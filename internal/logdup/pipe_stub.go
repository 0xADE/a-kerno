//go:build !linux

package logdup

import (
	"fmt"
	"os"
)

// osPipe returns an error on non-Linux platforms.
func osPipe() (*os.File, *os.File, error) {
	return nil, nil, fmt.Errorf("logdup: os.Pipe not available on this platform")
}
