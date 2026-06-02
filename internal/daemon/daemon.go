// Package daemon provides daemon lifecycle management for a-kerno.
// It defines the Daemon type representing a single managed daemon process,
// its state machine, and thread-safe accessors.
package daemon

import (
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// DaemonState represents the operational state of a daemon process.
type DaemonState int

const (
	// DaemonStopped is the initial state; daemon is not running.
	DaemonStopped DaemonState = iota
	// DaemonStarting means the process has been launched but readiness is not yet confirmed.
	DaemonStarting
	// DaemonRunning means the daemon is running and its socket (if configured) accepts connections.
	DaemonRunning
	// DaemonFailed means the daemon exited with an error and will not be restarted.
	DaemonFailed
	// DaemonDisabled means the daemon is not enabled in the configuration.
	DaemonDisabled
)

// stateNames maps DaemonState values to human-readable names for logging.
var stateNames = map[DaemonState]string{
	DaemonStopped:  "stopped",
	DaemonStarting: "starting",
	DaemonRunning:  "running",
	DaemonFailed:   "failed",
	DaemonDisabled: "disabled",
}

// String returns the human-readable name of the daemon state.
func (s DaemonState) String() string {
	if name, ok := stateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", s)
}

// Daemon represents a single managed daemon process.
// All exported fields except mu are safe to read under the read lock.
type Daemon struct {
	// Name is the daemon identifier (matches the section heading in daemons.md).
	Name string

	// Config is the static configuration for this daemon (loaded from daemons.md).
	Config DaemonConfig

	// State is the current operational state.
	State DaemonState

	// Cmd is the exec.Cmd handle for the running process, or nil if not running.
	Cmd *exec.Cmd

	// PID is the process ID of the running daemon, or 0 if not running.
	PID int

	// PGID is the process group ID (equal to PID when Setpgid is used with no
	// child processes spawning sub-groups).
	PGID int

	// StartedAt records the time of the most recent successful start.
	StartedAt time.Time

	// StoppedAt records the time the daemon was last stopped.
	StoppedAt time.Time

	// RestartCount is the number of times this daemon has been restarted
	// during the current a-kerno session.
	RestartCount int

	// Logs is a ring buffer holding the most recent log lines from the
	// daemon's stdout/stderr, used by the "logs" management command.
	Logs *LogBuffer

	// logWriters holds the pipe writers for stdout/stderr duplication.
	// Set during Start(); closed during Stop().
	logWriters *logWriters

	mu sync.RWMutex
}

// logWriters holds the write ends of the logdup pipes for a daemon.
type logWriters struct {
	stdout io.WriteCloser
	stderr io.WriteCloser
	close  func()
}

// SetState sets the daemon state in a thread-safe manner.
func (d *Daemon) SetState(state DaemonState) {
	d.mu.Lock()
	d.State = state
	d.mu.Unlock()
}

// GetState returns the current daemon state in a thread-safe manner.
func (d *Daemon) GetState() DaemonState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.State
}

// IsRunning returns true if the daemon is in the Running state.
func (d *Daemon) IsRunning() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.State == DaemonRunning
}

// Uptime returns the duration the daemon has been running.
// If the daemon is not in the Running state, it returns 0.
func (d *Daemon) Uptime() time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.State != DaemonRunning || d.StartedAt.IsZero() {
		return 0
	}
	return time.Since(d.StartedAt)
}

// SetRestartPolicy atomically updates the daemon's restart policy.
func (d *Daemon) SetRestartPolicy(policy RestartPolicy) {
	d.mu.Lock()
	d.Config.Restart = policy
	d.mu.Unlock()
}

// String returns a human-readable representation of the daemon for logging.
func (d *Daemon) String() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return fmt.Sprintf("daemon %q (state=%s pid=%d restarts=%d)",
		d.Name, d.State, d.PID, d.RestartCount)
}
