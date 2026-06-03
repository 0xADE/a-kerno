package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/0xADE/a-kerno/internal/config"
	"github.com/0xADE/a-kerno/internal/logdup"
	"github.com/fsnotify/fsnotify"

	"golang.org/x/sync/errgroup"
)

// DefaultLogBufferSize is the default capacity (number of lines) for a
// daemon's LogBuffer.
const DefaultLogBufferSize = 4096

// DaemonManager manages the lifecycle of all daemons defined in daemons.md.
// It handles ordered startup, parallel launch of same-order daemons,
// graceful shutdown (reverse order), and readiness probing via Unix sockets.
type DaemonManager struct {
	configs []DaemonConfig
	daemons map[string]*Daemon // name → *Daemon
	mu      sync.RWMutex
	cfg     *config.Config
	logger  *slog.Logger
	uid     string
	home    string
	dmPath  string // path to daemons.md
}

// NewDaemonManager creates a new DaemonManager with the given configuration
// and parsed daemon configs slice. It initializes the daemon map but does not
// start any processes.
func NewDaemonManager(cfg *config.Config, configs []DaemonConfig, uid, home string) *DaemonManager {
	logger := slog.Default().With("component", "daemon-manager")
	dm := &DaemonManager{
		configs: configs,
		daemons: make(map[string]*Daemon, len(configs)),
		cfg:     cfg,
		logger:  logger,
		uid:     uid,
		home:    home,
		dmPath:  cfg.DaemonsMD,
	}

	for i := range configs {
		dc := &configs[i]
		d := &Daemon{
			Name:   dc.Name,
			Config: *dc,
			Logs:   NewLogBuffer(DefaultLogBufferSize),
		}
		if !dc.Enabled {
			d.State = DaemonDisabled
		}
		dm.daemons[dc.Name] = d
	}

	return dm
}

// StartAll launches all enabled daemons in order. Daemons with the same Order
// value are started concurrently via errgroup. After all daemons are launched,
// it waits for each daemon's socket readiness (if a socket path is configured).
func (m *DaemonManager) StartAll(ctx context.Context) error {
	// Group daemons by order value.
	orderGroups := m.groupByOrder()

	for _, group := range orderGroups {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		g, gCtx := errgroup.WithContext(ctx)
		for _, name := range group.names {
			name := name // capture for closure
			g.Go(func() error {
				return m.Start(gCtx, name)
			})
		}
		if err := g.Wait(); err != nil {
			return fmt.Errorf("start group order=%d: %w", group.order, err)
		}
	}

	return nil
}

// orderGroup holds the daemon names that share the same order value.
type orderGroup struct {
	order int
	names []string
}

// groupByOrder returns daemon names grouped by their Order value, in ascending order.
func (m *DaemonManager) groupByOrder() []orderGroup {
	// Collect enabled daemons with their order.
	type entry struct {
		name  string
		order int
	}
	var entries []entry
	for _, dc := range m.configs {
		if !dc.Enabled {
			continue
		}
		entries = append(entries, entry{name: dc.Name, order: dc.Order})
	}

	if len(entries) == 0 {
		return nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].order < entries[j].order
	})

	var groups []orderGroup
	for _, e := range entries {
		if len(groups) == 0 || groups[len(groups)-1].order != e.order {
			groups = append(groups, orderGroup{order: e.order})
		}
		groups[len(groups)-1].names = append(groups[len(groups)-1].names, e.name)
	}

	return groups
}

// StopAll stops all running daemons in reverse order (highest Order first).
// It sends SIGTERM to each process group, waits up to 10 seconds, then sends
// SIGKILL if the process has not yet exited.
func (m *DaemonManager) StopAll(ctx context.Context) error {
	m.mu.RLock()
	// Collect enabled daemons sorted by Order descending.
	type entry struct {
		name  string
		order int
	}
	var entries []entry
	for _, d := range m.daemons {
		if d.Config.Enabled && d.IsRunning() {
			entries = append(entries, entry{name: d.Name, order: d.Config.Order})
		}
	}
	m.mu.RUnlock()

	// Sort descending: highest order stops first.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].order > entries[j].order
	})

	var firstErr error
	for _, e := range entries {
		if err := m.Stop(ctx, e.name); err != nil {
			slog.Error("failed to stop daemon during shutdown",
				"daemon", e.name,
				"error", err,
			)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// Start launches a single daemon by name. It creates the exec.Cmd with
// Setpgid: true, sets up log duplication via logdup, starts the process,
// records PID/PGID, waits for socket readiness, and launches a monitor
// goroutine for restart policies.
func (m *DaemonManager) Start(ctx context.Context, name string) error {
	d := m.Get(name)
	if d == nil {
		return fmt.Errorf("daemon %q not found", name)
	}

	d.mu.Lock()
	if d.State == DaemonRunning || d.State == DaemonStarting {
		d.mu.Unlock()
		slog.Info("daemon already running or starting, skipping", "daemon", name)
		return nil
	}
	if d.Config.Enabled && d.State != DaemonStopped && d.State != DaemonFailed && d.State != DaemonDisabled {
		d.mu.Unlock()
		return fmt.Errorf("daemon %q is in state %s; cannot start", name, d.State)
	}
	d.State = DaemonStarting
	cfg := d.Config // copy under lock
	d.mu.Unlock()

	// Build the command.
	//nolint:gosec // Exec comes from trusted daemons.md config
	cmd := exec.CommandContext(ctx, cfg.Exec)
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

	// Set up log duplication via logdup.
	daemonLogger := slog.Default().With("daemon", name)
	writers, err := logdup.Duplicate(name, daemonLogger, func(line string) {
		// Also write each line to the daemon's ring buffer.
		if d.Logs != nil {
			d.Logs.Append(line)
		}
	})
	if err != nil {
		d.SetState(DaemonFailed)
		return fmt.Errorf("logdup for %q: %w", name, err)
	}
	cmd.Stdout = writers.Stdout
	cmd.Stderr = writers.Stderr

	slog.Info("starting daemon", "daemon", name, "exec", cfg.Exec, "order", cfg.Order)

	if err := cmd.Start(); err != nil {
		writers.Close()
		d.SetState(DaemonFailed)
		return fmt.Errorf("start %q: %w", name, err)
	}

	pid := cmd.Process.Pid

	d.mu.Lock()
	d.Cmd = cmd
	d.PID = pid
	d.PGID = pid // Setpgid creates a new process group with PGID == PID
	d.StartedAt = time.Now()
	d.logWriters = &logWriters{
		stdout: writers.Stdout,
		stderr: writers.Stderr,
		close:  writers.Close,
	}
	d.mu.Unlock()

	// Wait for socket readiness if a socket path is configured.
	if cfg.Socket != "" {
		slog.Info("waiting for daemon socket", "daemon", name, "socket", cfg.Socket, "timeout", cfg.ReadyTimeout)
		if err := m.waitForSocket(cfg.Socket, cfg.ReadyTimeout); err != nil {
			d.SetState(DaemonFailed)
			return fmt.Errorf("daemon %q socket readiness: %w", name, err)
		}
	}

	d.SetState(DaemonRunning)
	slog.Info("daemon started successfully", "daemon", name, "pid", pid)

	// Launch monitor goroutine for restart policies.
	go m.monitorDaemon(ctx, name)

	return nil
}

// Stop stops a single daemon by name. It sends SIGTERM to the process group,
// waits up to 10 seconds for the process to exit, then sends SIGKILL if
// the process is still alive. Log writers are closed after the process exits.
func (m *DaemonManager) Stop(ctx context.Context, name string) error {
	d := m.Get(name)
	if d == nil {
		return fmt.Errorf("daemon %q not found", name)
	}

	d.mu.RLock()
	if d.State != DaemonRunning && d.State != DaemonStarting {
		d.mu.RUnlock()
		slog.Info("daemon not running, nothing to stop", "daemon", name, "state", d.State)
		return nil
	}
	pgid := d.PGID
	d.mu.RUnlock()

	slog.Info("stopping daemon", "daemon", name, "pgid", pgid)

	// Send SIGTERM to the process group.
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		slog.Warn("SIGTERM failed", "daemon", name, "error", err)
	}

	// Wait for the process to exit with a 10-second timeout.
	const stopTimeout = 10 * time.Second
	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		if !m.isProcessAlive(pgid) {
			m.cleanupDaemon(d)
			slog.Info("daemon stopped gracefully", "daemon", name)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Force kill if still alive.
	slog.Warn("daemon did not stop in time, sending SIGKILL", "daemon", name)
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("SIGKILL %q (pgid=%d): %w", name, pgid, err)
	}

	// Brief wait for SIGKILL to take effect.
	time.Sleep(100 * time.Millisecond)

	m.cleanupDaemon(d)
	slog.Info("daemon force-killed", "daemon", name)
	return nil
}

// cleanupDaemon resets the daemon state after the process has exited and
// closes the log writers.
func (m *DaemonManager) cleanupDaemon(d *Daemon) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.State = DaemonStopped
	d.StoppedAt = time.Now()
	d.Cmd = nil
	d.PID = 0
	d.PGID = 0

	if d.logWriters != nil {
		d.logWriters.close()
		d.logWriters = nil
	}
}

// Get returns the Daemon with the given name, or nil if not found.
func (m *DaemonManager) Get(name string) *Daemon {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.daemons[name]
}

// List returns a slice of all managed daemons.
func (m *DaemonManager) List() []*Daemon {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Daemon, 0, len(m.daemons))
	for _, d := range m.daemons {
		result = append(result, d)
	}
	return result
}

// waitForSocket polls the given Unix socket path by attempting a connection
// every 100ms until the timeout expires. Returns nil when the socket accepts
// a connection.
func (m *DaemonManager) waitForSocket(socketPath string, timeout time.Duration) error {
	if socketPath == "" || timeout <= 0 {
		return nil
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(context.Background(), "unix", socketPath)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for socket %s (%v)", socketPath, timeout)
}

// isProcessAlive checks whether a process with the given PGID still exists
// by sending signal 0 (null signal) to the process group.
func (m *DaemonManager) isProcessAlive(pgid int) bool {
	err := syscall.Kill(-pgid, syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

// ConfigPath returns the path to the daemons.md configuration file
// being watched/monitored.
func (m *DaemonManager) ConfigPath() string {
	return m.dmPath
}

// UID returns the user ID used for variable expansion.
func (m *DaemonManager) UID() string {
	return m.uid
}

// Home returns the home directory used for variable expansion.
func (m *DaemonManager) Home() string {
	return m.home
}

// WatchConfig watches the daemons.md configuration file for changes using
// fsnotify. When a change is detected, it reloads the config, compares with
// the current daemon list, starts new daemons, and stops removed ones.
// A 1-second debounce prevents reacting to duplicate events.
//
// The caller should pass a cancellable context to stop watching.
func (m *DaemonManager) WatchConfig(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify: create watcher: %w", err)
	}

	// Add the daemons.md file to the watcher.
	if err := watcher.Add(m.dmPath); err != nil {
		watcher.Close()
		return fmt.Errorf("fsnotify: watch %s: %w", m.dmPath, err)
	}

	m.logger.Info("watching config file for changes", "path", m.dmPath)

	go func() {
		defer watcher.Close()

		// Debounce timer: coalesce rapid successive events within 1 second.
		var debounceTimer *time.Timer
		const debounceInterval = 1 * time.Second

		for {
			select {
			case <-ctx.Done():
				// Stop any pending debounce timer.
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				return

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Only react to Write and Create events.
				if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
					continue
				}

				m.logger.Info("config file changed, scheduling reload",
					"path", m.dmPath,
					"op", event.Op.String(),
				)

				// Reset debounce timer.
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(debounceInterval, func() {
					if err := m.reloadConfig(ctx); err != nil {
						m.logger.Error("failed to reload config", "error", err)
					}
				})

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				m.logger.Error("fsnotify error", "error", err)
			}
		}
	}()

	return nil
}

// reloadConfig re-reads daemons.md, compares the new config with the
// current state, starts new daemons, and stops removed daemons.
func (m *DaemonManager) reloadConfig(ctx context.Context) error {
	m.logger.Info("reloading daemon configuration", "path", m.dmPath)

	// Check if the file still exists (it may have been deleted).
	if _, err := os.Stat(m.dmPath); os.IsNotExist(err) {
		m.logger.Warn("config file removed, keeping current daemon list", "path", m.dmPath)
		return nil
	}

	// Load new configuration.
	newConfigs, err := LoadConfig(m.dmPath, m.uid, m.home)
	if err != nil {
		return fmt.Errorf("reload: parse %s: %w", m.dmPath, err)
	}

	// Build lookup maps.
	newByName := make(map[string]DaemonConfig, len(newConfigs))
	for _, nc := range newConfigs {
		newByName[nc.Name] = nc
	}

	m.mu.RLock()
	oldByName := make(map[string]bool, len(m.daemons))
	for name := range m.daemons {
		oldByName[name] = true
	}
	m.mu.RUnlock()

	// Detect added daemons.
	var added []DaemonConfig
	for name, nc := range newByName {
		if !oldByName[name] {
			added = append(added, nc)
		}
	}

	// Detect removed daemons.
	var removed []string
	for name := range oldByName {
		if _, exists := newByName[name]; !exists {
			removed = append(removed, name)
		}
	}

	m.logger.Info("config diff",
		"added", len(added),
		"removed", len(removed),
		"total_new", len(newConfigs),
	)

	// Stop removed daemons.
	for _, name := range removed {
		m.logger.Info("daemon removed from config, stopping", "daemon", name)
		if err := m.Stop(ctx, name); err != nil {
			m.logger.Error("failed to stop removed daemon", "daemon", name, "error", err)
		}
		m.mu.Lock()
		delete(m.daemons, name)
		m.mu.Unlock()
	}

	// Add and start new daemons.
	for _, nc := range added {
		m.logger.Info("new daemon detected in config, adding", "daemon", nc.Name)

		dc := nc
		d := &Daemon{
			Name:   dc.Name,
			Config: dc,
			Logs:   NewLogBuffer(DefaultLogBufferSize),
		}
		if !dc.Enabled {
			d.State = DaemonDisabled
		}

		m.mu.Lock()
		m.daemons[dc.Name] = d
		m.mu.Unlock()

		if dc.Enabled {
			if err := m.Start(ctx, dc.Name); err != nil {
				m.logger.Error("failed to start new daemon", "daemon", dc.Name, "error", err)
			}
		}
	}

	// Update configs slice.
	m.mu.Lock()
	m.configs = newConfigs
	m.mu.Unlock()

	return nil
}
