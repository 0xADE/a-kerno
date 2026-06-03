package daemon

import (
	"sync"
)

// LogBuffer is a thread-safe ring buffer of log lines with a maximum
// capacity. When full, the oldest lines are silently overwritten.
type LogBuffer struct {
	lines   []string
	maxSize int
	pos     int // next write position
	full    bool
	mu      sync.RWMutex
}

// NewLogBuffer creates a new LogBuffer with the given maximum number of lines.
// maxSize must be >= 1; if <= 0 it defaults to 1.
func NewLogBuffer(maxSize int) *LogBuffer {
	if maxSize <= 0 {
		maxSize = 1
	}
	return &LogBuffer{
		lines:   make([]string, maxSize),
		maxSize: maxSize,
	}
}

// Append adds a single line to the buffer. If the buffer is full, the
// oldest line is overwritten.
func (lb *LogBuffer) Append(line string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	lb.lines[lb.pos] = line
	lb.pos++
	if lb.pos >= lb.maxSize {
		lb.pos = 0
		lb.full = true
	}
}

// Tail returns the last n lines from the buffer, in chronological order
// (oldest first). If n is <= 0, it defaults to 50. If there are fewer
// than n lines in the buffer, all available lines are returned.
func (lb *LogBuffer) Tail(n int) []string {
	if n <= 0 {
		n = 50
	}

	lb.mu.RLock()
	defer lb.mu.RUnlock()

	size := lb.pos
	if lb.full {
		size = lb.maxSize
	}
	if n > size {
		n = size
	}

	result := make([]string, n)
	for i := 0; i < n; i++ {
		// Read from the oldest line forward.
		idx := lb.pos - size + i
		if idx < 0 {
			idx += lb.maxSize
		}
		if idx >= lb.maxSize {
			idx -= lb.maxSize
		}
		result[i] = lb.lines[idx]
	}
	return result
}

// Len returns the number of lines currently in the buffer.
func (lb *LogBuffer) Len() int {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	if lb.full {
		return lb.maxSize
	}
	return lb.pos
}

// Clear empties the buffer.
func (lb *LogBuffer) Clear() {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.pos = 0
	lb.full = false
	// Zero out existing entries so the GC can collect them.
	for i := range lb.lines {
		lb.lines[i] = ""
	}
}
