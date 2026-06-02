//go:build !linux

// Package logdup provides a stub implementation for non-Linux platforms.
package logdup

import (
	"fmt"
	"io"
	"log/slog"
)

// Duplicate returns an error on non-Linux platforms.
func Duplicate(name string, cmdLogger *slog.Logger, lineCb func(line string)) (*LogWriters, error) {
	return nil, fmt.Errorf("logdup: not supported on this platform (daemon=%q)", name)
}

// LogWriters is a stub type for non-Linux platforms.
type LogWriters struct {
	Stdout io.WriteCloser
	Stderr io.WriteCloser
}

// Close is a no-op on non-Linux platforms.
func (w *LogWriters) Close() {}
