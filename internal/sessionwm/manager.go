package sessionwm

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// State is the lifecycle state of the session WM.
type State string

const (
	StateIdle     State = "idle"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateStopped  State = "stopped"
	StateFailed   State = "failed"
)

// Manager starts, monitors, and restarts the session compositor/WM.
type Manager struct {
	cfg    Config
	logger *slog.Logger

	mu           sync.Mutex
	state        State
	cmd          *exec.Cmd
	pid          int
	pgid         int
	restartCount int
	stopping     bool
	startedAt    time.Time
}

// NewManager creates a session WM manager from config.
func NewManager(cfg Config) *Manager {
	return &Manager{
		cfg:    cfg,
		logger: slog.Default().With("component", "sessionwm"),
		state:  StateIdle,
	}
}

// Enabled reports whether a WM command is configured.
func (m *Manager) Enabled() bool {
	return m.cfg.Spec != ""
}

// State returns the current WM state.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Spec returns the configured command string.
func (m *Manager) Spec() string {
	return m.cfg.Spec
}

// Start launches the session WM if configured. No-op when Spec is empty.
// The first start failure is returned as an error (fatal for the session).
func (m *Manager) Start(ctx context.Context) error {
	if !m.Enabled() {
		m.logger.Info("no ADE_COMPOSITOR/ADE_WM set; session WM disabled")
		return nil
	}

	m.mu.Lock()
	if m.state == StateRunning || m.state == StateStarting {
		m.mu.Unlock()
		return nil
	}
	m.stopping = false
	m.mu.Unlock()

	return m.startProcess(ctx, true)
}

// Stop terminates the session WM and disables further restarts until Start.
func (m *Manager) Stop(ctx context.Context) error {
	if !m.Enabled() {
		return nil
	}

	m.mu.Lock()
	m.stopping = true
	m.cfg.Restart = RestartDisabled
	pgid := m.pgid
	state := m.state
	m.state = StateStopping
	m.mu.Unlock()

	if state != StateRunning && state != StateStarting {
		m.mu.Lock()
		m.state = StateStopped
		m.mu.Unlock()
		return nil
	}

	m.logger.Info("stopping session WM", "pgid", pgid)
	if pgid != 0 {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	}

	const stopTimeout = 10 * time.Second
	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(pgid) {
			m.cleanupLocked(StateStopped)
			return nil
		}
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			m.cleanupLocked(StateStopped)
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	m.logger.Warn("session WM did not stop; sending SIGKILL", "pgid", pgid)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
	m.cleanupLocked(StateStopped)
	return nil
}

// Restart stops then starts the session WM (manual restart; keeps restart policy).
func (m *Manager) Restart(ctx context.Context) error {
	if !m.Enabled() {
		return fmt.Errorf("session WM not configured")
	}
	policy := m.cfg.Restart
	if err := m.Stop(ctx); err != nil {
		m.logger.Warn("stop before restart", "error", err)
	}
	m.mu.Lock()
	m.cfg.Restart = policy
	m.stopping = false
	m.restartCount = 0
	m.mu.Unlock()
	return m.startProcess(ctx, true)
}

func (m *Manager) startProcess(ctx context.Context, fatalOnFail bool) error {
	argv := ParseSpec(m.cfg.Spec)
	if len(argv) == 0 {
		return fmt.Errorf("empty session WM command")
	}

	m.mu.Lock()
	m.state = StateStarting
	m.mu.Unlock()

	//nolint:gosec // command comes from ADE_COMPOSITOR / ADE_WM trusted session env
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	m.logger.Info("starting session WM", "exec", m.cfg.Spec, "restart", m.cfg.Restart)

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		m.state = StateFailed
		m.mu.Unlock()
		if fatalOnFail {
			return fmt.Errorf("start session WM %q: %w", argv[0], err)
		}
		return err
	}

	pid := cmd.Process.Pid
	m.mu.Lock()
	m.cmd = cmd
	m.pid = pid
	m.pgid = pid
	m.startedAt = time.Now()
	m.state = StateRunning
	m.mu.Unlock()

	m.logger.Info("session WM started", "pid", pid)
	go m.monitor(ctx)
	return nil
}

func (m *Manager) monitor(ctx context.Context) {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	if cmd == nil {
		return
	}

	err := cmd.Wait()

	m.mu.Lock()
	stopping := m.stopping
	policy := m.cfg.Restart
	restartCount := m.restartCount
	m.cmd = nil
	m.pid = 0
	m.pgid = 0
	m.mu.Unlock()

	if stopping {
		m.mu.Lock()
		m.state = StateStopped
		m.mu.Unlock()
		return
	}

	if !shouldRestart(policy, err) {
		m.logger.Info("session WM exited; not restarting", "error", err, "policy", policy)
		m.mu.Lock()
		if err != nil {
			m.state = StateFailed
		} else {
			m.state = StateStopped
		}
		m.mu.Unlock()
		return
	}

	delay := nextRestartDelay(restartCount)
	m.logger.Info("session WM exited; scheduling restart",
		"error", err, "delay", delay, "restart_count", restartCount,
	)

	select {
	case <-ctx.Done():
		m.mu.Lock()
		m.state = StateStopped
		m.mu.Unlock()
		return
	case <-time.After(delay):
	}

	m.mu.Lock()
	if m.stopping {
		m.state = StateStopped
		m.mu.Unlock()
		return
	}
	m.restartCount++
	m.mu.Unlock()

	if startErr := m.startProcess(ctx, false); startErr != nil {
		m.logger.Error("session WM restart failed", "error", startErr)
		m.mu.Lock()
		m.state = StateFailed
		m.mu.Unlock()
	}
}

func (m *Manager) cleanupLocked(state State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
	m.cmd = nil
	m.pid = 0
	m.pgid = 0
}

func shouldRestart(policy RestartPolicy, exitErr error) bool {
	switch policy {
	case RestartAlways:
		return true
	case RestartOnFailure:
		return exitErr != nil
	case RestartDisabled:
		return false
	default:
		return false
	}
}

func nextRestartDelay(restartCount int) time.Duration {
	if restartCount <= 0 {
		return time.Second
	}
	delay := time.Duration(math.Pow(2, float64(restartCount))) * time.Second
	const maxDelay = 30 * time.Second
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func processAlive(pgid int) bool {
	if pgid == 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil
}
