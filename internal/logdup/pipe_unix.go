//go:build linux

package logdup

import "os"

// osPipe returns a pipe pair using os.Pipe().
func osPipe() (*os.File, *os.File, error) {
	return os.Pipe()
}
