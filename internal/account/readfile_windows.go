//go:build windows

package account

import "os"

// openForRead opens path for reading. The open does not wait on Windows: a file
// in a folder cannot be a FIFO there (named pipes have a namespace of their own),
// and what is not a regular file is refused by readStoreFile.
func openForRead(path string) (*os.File, error) {
	return os.Open(path)
}
