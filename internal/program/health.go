package program

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// RunHealthCheck executes the configured health check command for a program.
// It runs the health check up to HealthRetry times, waiting HealthTimeout
// seconds for each attempt. On success, sets state to ProgHealthy.
// On failure, sets state to ProgHealthFailed.
func RunHealthCheck(ctx context.Context, prog *Program) error {
	if prog.Config.HealthCheck == "" {
		// No health check configured; mark healthy immediately.
		prog.SetState(ProgHealthy)
		prog.mu.Lock()
		prog.Health = HealthOK
		prog.mu.Unlock()
		return nil
	}

	prog.mu.Lock()
	prog.Health = HealthChecking
	timeout := prog.Config.HealthTimeout
	if timeout <= 0 {
		timeout = DefaultHealthTimeout
	}
	retry := prog.Config.HealthRetry
	if retry <= 0 {
		retry = DefaultHealthRetry
	}
	prog.mu.Unlock()

	for attempt := 0; attempt < retry; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		checkCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		err := executeHealthCheck(checkCtx, prog.Config.HealthCheck)
		cancel()

		if err == nil {
			prog.mu.Lock()
			prog.Health = HealthOK
			prog.mu.Unlock()
			prog.SetState(ProgHealthy)
			return nil
		}

		// Wait a short time before retrying.
		time.Sleep(1 * time.Second)
	}

	prog.mu.Lock()
	prog.Health = HealthFailed
	prog.mu.Unlock()
	prog.SetState(ProgHealthFailed)
	return fmt.Errorf("health check failed for %q after %d attempts", prog.Name, retry)
}

// executeHealthCheck runs a health check command (shell invocation)
// and returns nil on success (exit code 0).
func executeHealthCheck(ctx context.Context, command string) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd.Run()
}
