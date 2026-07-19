package sessionwm

import (
	"context"
	"testing"
	"time"
)

func TestManagerStartStop(t *testing.T) {
	m := NewManager(Config{
		Spec:    "sleep 30",
		Restart: RestartDisabled,
	})
	ctx := context.Background()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m.State() != StateRunning {
		t.Fatalf("state = %s, want running", m.State())
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.State() == StateStopped {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("state = %s after stop, want stopped", m.State())
}

func TestManagerDisabledNoop(t *testing.T) {
	m := NewManager(Config{})
	if m.Enabled() {
		t.Fatal("expected disabled")
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestManagerStartMissingBinary(t *testing.T) {
	m := NewManager(Config{
		Spec:    "ade-nonexistent-wm-binary-xyz",
		Restart: RestartDisabled,
	})
	err := m.Start(context.Background())
	if err == nil {
		t.Fatal("expected start error")
	}
	if m.State() != StateFailed {
		t.Fatalf("state = %s, want failed", m.State())
	}
}

func TestManagerRestartsOnExit(t *testing.T) {
	// Use a short-lived process; always restart once then stop.
	m := NewManager(Config{
		Spec:    "true",
		Restart: RestartAlways,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait until restartCount increases (monitor saw exit and restarted).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := m.restartCount
		m.mu.Unlock()
		if n >= 1 {
			cancel()
			_ = m.Stop(context.Background())
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("WM was not restarted after exit")
}
