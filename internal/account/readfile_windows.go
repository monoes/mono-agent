//go:build windows

package account

import (
	"os"
	"syscall"
)

// openForRead opens path for reading. The open does not wait on Windows: a named
// pipe has a namespace of its own and is not a file of a folder, and a link in the
// folder that leads to a pipe or a device (making one takes a privilege) opens at
// once, or fails at once (a pipe with no free instance is ERROR_PIPE_BUSY).
// readStoreFile then refuses what the open file reports as not regular.
//
// The open shares delete as well as read and write, which os.Open does not: a
// reader that does not share delete makes the rename of a write fail with a
// sharing violation, and a rename in flight makes such an open fail with one.
func openForRead(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	share := uint32(syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE | syscall.FILE_SHARE_DELETE)
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, share, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
