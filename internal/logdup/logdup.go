//go:build linux

// Package logdup provides stdout/stderr duplication for daemon child processes.
// It captures daemon output through pipes, writes each line to the a-kerno
// slog logger with a "[daemon-name] " prefix, and also records lines in
// the daemon's LogBuffer ring buffer for the "logs" management command.
//
// The implementation is inspired by a-lancxo/internal/logdup/logdup.go but
// adapted for per-daemon pipe-based capture rather than global stdout/stderr
// redirection.
package logdup

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// Duplicate creates stdout and stderr pipes for a daemon child process,
// reads each output line in a goroutine, and writes it to the provided
// logger with a "[name] " prefix. The returned *LogWriters provide the
// write ends of the pipes; attach them to cmd.StdoutPipe / cmd.StderrPipe
// AFTER calling Duplicate (use the writers directly as cmd.Stdout / cmd.Stderr
// to avoid the default pipe behaviour).
//
// Usage:
//
//	writers, err := logdup.Duplicate("a-lancxo", cmd, logger)
//	cmd.Stdout = writers.Stdout
//	cmd.Stderr = writers.Stderr
//	cmd.Start()
func Duplicate(name string, cmdLogger *slog.Logger, lineCb func(line string)) (*LogWriters, error) {
	prOut, pwOut, err := osPipe()
	if err != nil {
		return nil, fmt.Errorf("logdup: stdout pipe for %q: %w", name, err)
	}

	prErr, pwErr, err := osPipe()
	if err != nil {
		_ = prOut.Close()
		_ = pwOut.Close()
		return nil, fmt.Errorf("logdup: stderr pipe for %q: %w", name, err)
	}

	prefix := fmt.Sprintf("[%s] ", name)

	var wg sync.WaitGroup
	wg.Add(2)

	// Copy stdout lines to the logger.
	go func() {
		defer wg.Done()
		scanLines(prOut, prefix, cmdLogger, lineCb)
	}()

	// Copy stderr lines to the logger.
	go func() {
		defer wg.Done()
		scanLines(prErr, prefix, cmdLogger, lineCb)
	}()

	return &LogWriters{
		Stdout: pwOut,
		Stderr: pwErr,
		wg:     &wg,
		prOut:  prOut,
		prErr:  prErr,
	}, nil
}

// LogWriters holds the write ends of the stdout/stderr pipes and the
// associated read-end cleanup state.
type LogWriters struct {
	Stdout io.WriteCloser
	Stderr io.WriteCloser
	wg     *sync.WaitGroup
	prOut  io.ReadCloser
	prErr  io.ReadCloser
}

// Close closes the write ends of both pipes and waits for the copy
// goroutines to finish. Call this after the daemon process exits to
// drain any remaining output.
func (w *LogWriters) Close() {
	_ = w.Stdout.Close()
	_ = w.Stderr.Close()
	w.wg.Wait()
	_ = w.prOut.Close()
	_ = w.prErr.Close()
}

// scanLines reads lines from the reader, prefixes each line with the
// daemon name, and writes it to the logger. It also invokes the optional
// lineCb callback for each line (used to feed the LogBuffer).
func scanLines(r io.Reader, prefix string, logger *slog.Logger, lineCb func(string)) {
	scanner := bufio.NewScanner(r)
	// Increase buffer for long lines.
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		logger.Info(prefix + line)
		if lineCb != nil {
			lineCb(line)
		}
	}
	// scanner.Err() is intentionally ignored; the pipe may be closed
	// abruptly when the daemon exits.
}
