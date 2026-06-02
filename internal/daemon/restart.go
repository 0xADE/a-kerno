package daemon

import (
	"context"
	"errors"
	"math"
	"time"
)

// ShouldRestart determines whether a daemon should be restarted based on
// its restart policy and the error returned by cmd.Wait().
func ShouldRestart(d *Daemon, exitErr error) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	switch d.Config.Restart {
	case RestartAlways:
		return true
	case RestartOnFailure:
		return exitErr != nil
	case RestartOnce:
		// restart only if this is the first failure
		return d.RestartCount == 0
	case RestartDisabled:
		return false
	default:
		return false
	}
}

// NextRestartDelay computes the delay before the next restart attempt
// using an exponential backoff strategy: 1s, 2s, 4s, 8s, 16s, 30s (cap).
func NextRestartDelay(restartCount int) time.Duration {
	if restartCount <= 0 {
		return time.Second
	}

	// Exponential: 2^restartCount seconds, capped at 30s.
	// restartCount=1 → 2s, restartCount=2 → 4s, restartCount=3 → 8s, restartCount=4 → 16s, restartCount=5+ → 30s.
	delay := time.Duration(math.Pow(2, float64(restartCount))) * time.Second
	const maxDelay = 30 * time.Second
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

// ResetRestartBackoff resets the restart counter if the daemon has been
// running successfully for more than 60 seconds. This prevents permanent
// backoff for daemons that experience transient failures.
func ResetRestartBackoff(d *Daemon) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.State != DaemonRunning {
		return
	}

	uptime := time.Since(d.StartedAt)
	if uptime > 60*time.Second {
		d.RestartCount = 0
	}
}

// monitorDaemon is a goroutine that waits for a daemon process to exit,
// checks the restart policy, and re-launches the daemon if applicable.
// It should be called from DaemonManager.Start() after a successful start.
func (m *DaemonManager) monitorDaemon(ctx context.Context, name string) {
	d := m.Get(name)
	if d == nil {
		return
	}

	d.mu.RLock()
	cmd := d.Cmd
	d.mu.RUnlock()

	if cmd == nil {
		return
	}

	// Wait for the process to exit.
	err := cmd.Wait()

	// Determine the daemon's disposition.
	d.mu.RLock()
	shouldRestart := ShouldRestart(d, err)
	d.mu.RUnlock()

	if err != nil {
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			// Already an ExitError, keep it.
		} else {
			// Wrap the error.
			exitErr = &ExitError{Err: err}
		}
	}

	if !shouldRestart {
		d.SetState(DaemonStopped)
		d.mu.Lock()
		d.StoppedAt = time.Now()
		d.Cmd = nil
		d.PID = 0
		d.PGID = 0
		if d.logWriters != nil {
			d.logWriters.close()
			d.logWriters = nil
		}
		d.mu.Unlock()
		return
	}

	// Compute backoff delay.
	d.mu.RLock()
	restartCount := d.RestartCount
	d.mu.RUnlock()

	delay := NextRestartDelay(restartCount)

	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}

	// Close log writers from previous process before restart.
	d.mu.Lock()
	if d.logWriters != nil {
		d.logWriters.close()
		d.logWriters = nil
	}
	d.RestartCount++
	d.Cmd = nil
	d.PID = 0
	d.PGID = 0
	d.mu.Unlock()

	if err := m.Start(ctx, name); err != nil {
		d.SetState(DaemonFailed)
	}
}

// ExitError wraps the error returned by cmd.Wait() to provide
// structured access to the exit code and signal information.
type ExitError struct {
	Err error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "exit error"
}

func (e *ExitError) Unwrap() error {
	return e.Err
}
