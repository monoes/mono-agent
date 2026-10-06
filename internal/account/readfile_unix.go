//go:build !windows

package account

import (
	"os"
	"syscall"
)

// openForRead opens path for reading without waiting: opening a FIFO read-only
// blocks until a writer opens it, and with O_NONBLOCK it returns at once, so that
// readStoreFile can look at what it opened and refuse it. A regular file is read
// as usual.
func openForRead(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
