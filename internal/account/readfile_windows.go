//go:build windows

package account

import "os"

// openForRead opens path for reading. The open does not wait on Windows: a named
// pipe has a namespace of its own and is not a file of a folder, and a link in the
// folder that leads to a pipe or a device (making one takes a privilege) opens at
// once, or fails at once (a pipe with no free instance is ERROR_PIPE_BUSY).
// readStoreFile then refuses what the open file reports as not regular.
func openForRead(path string) (*os.File, error) {
	return os.Open(path)
}
