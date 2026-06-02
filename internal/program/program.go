// Package program provides user program lifecycle management for a-kerno.
// It defines the Program type representing a single managed user program,
// its state machine (11 states from the specification), health status,
// and thread-safe accessors.
package program

import (
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// ProgramState represents the operational state of a user program.
// There are 11 states as defined in the a-kerno architecture document.
type ProgramState int

const (
	// ProgPending is the initial state; program is not yet scheduled.
	ProgPending ProgramState = iota
	// ProgWaitingDeps means the program is waiting for its dependencies to become healthy.
	ProgWaitingDeps
	// ProgWaitingDelay means the program is waiting for its configured start delay.
	ProgWaitingDelay
	// ProgStarting means the process has been launched but not yet confirmed running/healthy.
	ProgStarting
	// ProgRunning means the process is running but health check has not yet passed.
	ProgRunning
	// ProgHealthy means the process is running and health check passed.
	ProgHealthy
	// ProgDone means the process exited with code 0 and restart is disabled.
	ProgDone
	// ProgFailed means the process exited with a non-zero code or failed to start.
	ProgFailed
	// ProgHealthFailed means the process is running but health check failed.
	ProgHealthFailed
	// ProgStopped means the process was manually stopped.
	ProgStopped
	// ProgDisabled means the program is disabled in configuration.
	ProgDisabled
)

// stateNames maps ProgramState values to human-readable names.
var stateNames = map[ProgramState]string{
	ProgPending:      "pending",
	ProgWaitingDeps:  "waiting-deps",
	ProgWaitingDelay: "waiting-delay",
	ProgStarting:     "starting",
	ProgRunning:      "running",
	ProgHealthy:      "healthy",
	ProgDone:         "done",
	ProgFailed:       "failed",
	ProgHealthFailed: "health-failed",
	ProgStopped:      "stopped",
	ProgDisabled:     "disabled",
}

// String returns the human-readable name of the program state.
func (s ProgramState) String() string {
	if name, ok := stateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", s)
}

// HealthStatus represents the health check result for a program.
type HealthStatus int

const (
	// HealthUnknown means no health check has been performed yet.
	HealthUnknown HealthStatus = iota
	// HealthChecking means a health check is currently in progress.
	HealthChecking
	// HealthOK means the last health check passed.
	HealthOK
	// HealthFailed means the last health check failed.
	HealthFailed
)

// healthNames maps HealthStatus values to human-readable names.
var healthNames = map[HealthStatus]string{
	HealthUnknown:  "unknown",
	HealthChecking: "checking",
	HealthOK:       "ok",
	HealthFailed:   "failed",
}

// String returns the human-readable name of the health status.
func (h HealthStatus) String() string {
	if name, ok := healthNames[h]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", h)
}

// Program represents a single managed user program.
// All exported fields except mu are safe to read under the read lock.
type Program struct {
	// Name is the program identifier (file name without extension).
	Name string

	// Config is the static configuration for this program.
	Config ProgramConfig

	// State is the current operational state.
	State ProgramState

	// Cmd is the exec.Cmd handle for the running process, or nil if not running.
	Cmd *exec.Cmd

	// PID is the process ID of the running program, or 0 if not running.
	PID int

	// StartedAt records the time of the most recent successful start.
	StartedAt time.Time

	// FinishedAt records the time the program exited.
	FinishedAt time.Time

	// ExitCode is the exit code of the completed process, or 0 if still running.
	ExitCode int

	// Health is the current health check status.
	Health HealthStatus

	mu sync.RWMutex
}

// SetState sets the program state in a thread-safe manner.
func (p *Program) SetState(state ProgramState) {
	p.mu.Lock()
	p.State = state
	p.mu.Unlock()
}

// GetState returns the current program state in a thread-safe manner.
func (p *Program) GetState() ProgramState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.State
}

// IsRunning returns true if the program is in one of the running states
// (ProgRunning, ProgHealthy, ProgStarting, ProgHealthFailed).
func (p *Program) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	switch p.State {
	case ProgStarting, ProgRunning, ProgHealthy, ProgHealthFailed:
		return true
	}
	return false
}

// IsFinished returns true if the program is in a terminal state
// (ProgDone, ProgFailed, ProgStopped, ProgDisabled).
func (p *Program) IsFinished() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	switch p.State {
	case ProgDone, ProgFailed, ProgStopped, ProgDisabled:
		return true
	}
	return false
}
