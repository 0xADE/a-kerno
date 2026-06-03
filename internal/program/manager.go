package program

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

// ProgramManager manages the lifecycle of all user programs defined
// in autostart directories. It handles dependency resolution, phased
// startup, health checks, and graceful shutdown.
type ProgramManager struct {
	configs  []ProgramConfig
	programs map[string]*Program // name → *Program
	mu       sync.RWMutex
	logger   *slog.Logger
}

// NewProgramManager creates a new ProgramManager with the given configurations.
func NewProgramManager(configs []ProgramConfig) *ProgramManager {
	logger := slog.Default().With("component", "program-manager")
	pm := &ProgramManager{
		configs:  configs,
		programs: make(map[string]*Program, len(configs)),
		logger:   logger,
	}

	for i := range configs {
		pc := &configs[i]
		prog := &Program{
			Name:   pc.Name,
			Config: *pc,
		}
		if !pc.Enabled {
			prog.State = ProgDisabled
		} else {
			prog.State = ProgPending
		}
		pm.programs[pc.Name] = prog
	}

	return pm
}

// LoadConfigs loads program configurations from the given directories.
// This is a convenience wrapper around LoadProgramConfigs.
func LoadConfigs(autostartDir, xdgAutostartDir, uid, home string) ([]ProgramConfig, error) {
	return LoadProgramConfigs(autostartDir, xdgAutostartDir, uid, home)
}

// ResolveDependencies returns groups of program names in dependency order
// (topological sort). Each group contains programs that can be started in
// parallel. Returns an error on self-dependencies, missing dependencies,
// cross-phase violations (early cannot depend on post), or cycles.
func (m *ProgramManager) ResolveDependencies() ([][]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Build name→config map for enabled programs.
	enabled := make(map[string]ProgramConfig)
	for _, cfg := range m.configs {
		if cfg.Enabled {
			enabled[cfg.Name] = cfg
		}
	}

	// Build adjacency list and in-degree counts.
	graph := make(map[string][]string) // name → list of programs that depend on it
	inDegree := make(map[string]int)

	for name := range enabled {
		graph[name] = nil
		if _, ok := inDegree[name]; !ok {
			inDegree[name] = 0
		}
	}

	for name, cfg := range enabled {
		for _, dep := range cfg.DependsOn {
			// Check self-dependency.
			if dep == name {
				return nil, fmt.Errorf("program %q depends on itself", name)
			}

			depCfg, exists := enabled[dep]
			if !exists {
				return nil, fmt.Errorf("program %q depends on unknown program %q", name, dep)
			}

			// Cross-phase check: early cannot depend on post.
			if cfg.Phase == phaseEarly && depCfg.Phase == phasePost {
				return nil, fmt.Errorf("early program %q depends on post program %q", name, dep)
			}

			graph[dep] = append(graph[dep], name)
			inDegree[name]++
		}
	}

	// Kahn's algorithm for topological sort, respecting phase and priority.
	var groups [][]string
	remaining := make(map[string]bool)
	for name := range enabled {
		remaining[name] = true
	}

	for len(remaining) > 0 {
		// Collect nodes with in-degree 0, sorted by phase then priority then name.
		var ready []string
		for name := range remaining {
			if inDegree[name] == 0 {
				ready = append(ready, name)
			}
		}

		if len(ready) == 0 {
			// Cycle detected.
			var names []string
			for name := range remaining {
				names = append(names, name)
			}
			return nil, fmt.Errorf("dependency cycle detected among: %s", strings.Join(names, ", "))
		}

		// Sort ready programs: early first, then by priority, then by name.
		sort.Slice(ready, func(i, j int) bool {
			ai, aj := enabled[ready[i]], enabled[ready[j]]
			if ai.Phase != aj.Phase {
				if ai.Phase == phaseEarly {
					return true
				}
				if aj.Phase == phaseEarly {
					return false
				}
			}
			if ai.Priority != aj.Priority {
				return ai.Priority < aj.Priority
			}
			return ready[i] < ready[j]
		})

		groups = append(groups, ready)

		// Remove ready nodes from the graph.
		for _, name := range ready {
			delete(remaining, name)
			for _, dependent := range graph[name] {
				inDegree[dependent]--
			}
		}
	}

	return groups, nil
}

// StartPhase starts all programs in the given phase ("early" or "post"),
// respecting dependency order. Already-running programs are skipped.
func (m *ProgramManager) StartPhase(ctx context.Context, phase string) error {
	groups, err := m.ResolveDependencies()
	if err != nil {
		return fmt.Errorf("resolve deps: %w", err)
	}

	m.mu.RLock()
	phasePrograms := make(map[string]bool)
	for name, cfg := range m.programs {
		if cfg.Config.Phase == phase || (phase == "post" && cfg.Config.Phase == "") {
			if cfg.Config.Enabled {
				phasePrograms[name] = true
			}
		}
	}
	m.mu.RUnlock()

	if len(phasePrograms) == 0 {
		m.logger.Info("no programs to start in phase", "phase", phase)
		return nil
	}

	// Start each dependency group in order, with parallel launch within groups.
	for groupIdx, group := range groups {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Filter to only programs in the target phase.
		var toStart []string
		for _, name := range group {
			if phasePrograms[name] {
				toStart = append(toStart, name)
			}
		}

		if len(toStart) == 0 {
			continue
		}

		m.logger.Info("starting program group",
			"phase", phase,
			"group", groupIdx,
			"programs", toStart,
		)

		g, gCtx := errgroup.WithContext(ctx)
		for _, name := range toStart {
			name := name
			g.Go(func() error {
				return m.StartProgram(gCtx, name)
			})
		}
		if err := g.Wait(); err != nil {
			return fmt.Errorf("start group %d: %w", groupIdx, err)
		}
	}

	return nil
}

// StartProgram launches a single program by name. It handles dependency
// waiting, start delay, process launch, and health checking.
func (m *ProgramManager) StartProgram(ctx context.Context, name string) error {
	prog := m.Get(name)
	if prog == nil {
		return fmt.Errorf("program %q not found", name)
	}

	prog.mu.Lock()
	if prog.State == ProgRunning || prog.State == ProgHealthy || prog.State == ProgStarting {
		prog.mu.Unlock()
		m.logger.Info("program already running, skipping", "program", name)
		return nil
	}
	if prog.State == ProgDisabled {
		prog.mu.Unlock()
		m.logger.Info("program is disabled, skipping", "program", name)
		return nil
	}
	cfg := prog.Config
	prog.State = ProgWaitingDeps
	prog.mu.Unlock()

	// Wait for dependencies to become healthy.
	for _, depName := range cfg.DependsOn {
		dep := m.Get(depName)
		if dep == nil {
			return fmt.Errorf("dependency %q not found for program %q", depName, name)
		}

		m.logger.Info("waiting for dependency", "program", name, "depends_on", depName)
		if err := m.waitForHealthy(ctx, dep, 120*time.Second); err != nil {
			prog.SetState(ProgFailed)
			return fmt.Errorf("dependency %q not healthy: %w", depName, err)
		}
	}

	// Apply start delay if configured.
	if cfg.StartDelay > 0 {
		prog.SetState(ProgWaitingDelay)
		m.logger.Info("waiting start delay", "program", name, "delay_ms", cfg.StartDelay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(cfg.StartDelay) * time.Millisecond):
		}
	}

	prog.SetState(ProgStarting)

	// Build the command.
	//nolint:gosec // Exec comes from trusted config
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cfg.Exec)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Set environment variables.
	if len(cfg.Env) > 0 {
		env := cmd.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	// Discard stdout/stderr for programs (logdup is for daemons only).
	cmd.Stdout = nil
	cmd.Stderr = nil

	m.logger.Info("starting program", "program", name, "exec", cfg.Exec, "phase", cfg.Phase)

	if err := cmd.Start(); err != nil {
		prog.SetState(ProgFailed)
		return fmt.Errorf("start program %q: %w", name, err)
	}

	pid := cmd.Process.Pid

	prog.mu.Lock()
	prog.Cmd = cmd
	prog.PID = pid
	prog.StartedAt = time.Now()
	prog.State = ProgRunning
	prog.mu.Unlock()

	// Run health check if configured.
	if cfg.HealthCheck != "" {
		m.logger.Info("running health check for program", "program", name, "health_check", cfg.HealthCheck)
		go func() {
			if err := RunHealthCheck(ctx, prog); err != nil {
				m.logger.Warn("program health check failed", "program", name, "error", err)
			}
		}()
	} else {
		prog.SetState(ProgHealthy)
		prog.mu.Lock()
		prog.Health = HealthOK
		prog.mu.Unlock()
	}

	// Monitor process exit.
	go m.monitorProgram(ctx, name)

	return nil
}

// StopProgram stops a single program by name. It sends SIGTERM to the
// process group, waits up to 10 seconds, then sends SIGKILL.
func (m *ProgramManager) StopProgram(ctx context.Context, name string) error {
	prog := m.Get(name)
	if prog == nil {
		return fmt.Errorf("program %q not found", name)
	}

	prog.mu.RLock()
	if !prog.IsRunning() {
		prog.mu.RUnlock()
		m.logger.Info("program not running, nothing to stop", "program", name, "state", prog.State)
		return nil
	}
	pid := prog.PID
	prog.mu.RUnlock()

	m.logger.Info("stopping program", "program", name, "pid", pid)

	// Send SIGTERM.
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		m.logger.Warn("SIGTERM failed", "program", name, "error", err)
	}

	// Wait for exit.
	const stopTimeout = 10 * time.Second
	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		if !m.isProcessAlive(pid) {
			prog.SetState(ProgStopped)
			prog.mu.Lock()
			prog.FinishedAt = time.Now()
			prog.Cmd = nil
			prog.PID = 0
			prog.mu.Unlock()
			m.logger.Info("program stopped gracefully", "program", name)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Force kill.
	m.logger.Warn("program did not stop in time, sending SIGKILL", "program", name)
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("SIGKILL %q (pid=%d): %w", name, pid, err)
	}

	time.Sleep(100 * time.Millisecond)

	prog.SetState(ProgStopped)
	prog.mu.Lock()
	prog.FinishedAt = time.Now()
	prog.Cmd = nil
	prog.PID = 0
	prog.mu.Unlock()

	m.logger.Info("program force-killed", "program", name)
	return nil
}

// StopAll stops all running programs. Post-phase programs are stopped first,
// then early-phase programs.
func (m *ProgramManager) StopAll(ctx context.Context) error {
	m.mu.RLock()
	var postPrograms, earlyPrograms []string
	for name, prog := range m.programs {
		if !prog.IsRunning() {
			continue
		}
		if prog.Config.Phase == "early" {
			earlyPrograms = append(earlyPrograms, name)
		} else {
			postPrograms = append(postPrograms, name)
		}
	}
	m.mu.RUnlock()

	// Stop post programs first.
	for _, name := range postPrograms {
		if err := m.StopProgram(ctx, name); err != nil {
			m.logger.Error("failed to stop post program", "program", name, "error", err)
		}
	}

	// Then stop early programs.
	for _, name := range earlyPrograms {
		if err := m.StopProgram(ctx, name); err != nil {
			m.logger.Error("failed to stop early program", "program", name, "error", err)
		}
	}

	return nil
}

// Get returns the Program with the given name, or nil if not found.
func (m *ProgramManager) Get(name string) *Program {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.programs[name]
}

// List returns a slice of all managed programs.
func (m *ProgramManager) List() []*Program {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Program, 0, len(m.programs))
	for _, p := range m.programs {
		result = append(result, p)
	}
	return result
}

// waitForHealthy polls the given program until it reaches ProgHealthy state
// or the timeout expires.
func (m *ProgramManager) waitForHealthy(ctx context.Context, prog *Program, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if prog.GetState() == ProgHealthy {
			return nil
		}
		if prog.IsFinished() {
			return fmt.Errorf("dependency %q is in terminal state %s", prog.Name, prog.GetState())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for %q to become healthy", prog.Name)
}

// monitorProgram is a goroutine that waits for a program process to exit
// and updates the program state accordingly.
func (m *ProgramManager) monitorProgram(_ context.Context, name string) {
	prog := m.Get(name)
	if prog == nil {
		return
	}

	prog.mu.RLock()
	cmd := prog.Cmd
	restart := prog.Config.Restart
	prog.mu.RUnlock()

	if cmd == nil {
		return
	}

	err := cmd.Wait()

	prog.mu.Lock()
	prog.FinishedAt = time.Now()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			prog.ExitCode = exitErr.ExitCode()
		} else {
			prog.ExitCode = -1
		}
	} else {
		prog.ExitCode = 0
	}

	if prog.ExitCode == 0 && !restart {
		prog.State = ProgDone
	} else if prog.ExitCode != 0 {
		prog.State = ProgFailed
	}

	prog.Cmd = nil
	prog.PID = 0
	prog.mu.Unlock()

	if err != nil {
		m.logger.Warn("program exited with error",
			"program", name,
			"exit_code", prog.ExitCode,
			"error", err,
		)
	} else {
		m.logger.Info("program exited normally", "program", name)
	}
}

// isProcessAlive checks whether a process with the given PID still exists.
func (m *ProgramManager) isProcessAlive(pid int) bool {
	err := syscall.Kill(-pid, syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
