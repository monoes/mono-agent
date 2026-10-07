//go:build windows

package account

import (
	"errors"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// moveFileEx is windows.MoveFileEx, a variable so that a test can see the flags
// it is given.
var moveFileEx = windows.MoveFileEx

// replaceFile renames from over to and asks for the rename to be written through
// (MOVEFILE_WRITE_THROUGH, which Microsoft documents as returning once the move is
// on the disk). Windows has no directory flush (syncDir does nothing there), and
// os.Rename passes MOVEFILE_REPLACE_EXISTING alone: a power cut just after a
// refresh could then bring back the old refresh.enc, whose token monoes.me has
// rotated away, or a session.json without a record that had to be on the disk
// before the grant was sent, and the next start would present a dead token.
// Microsoft words the guarantee for a move done as a copy and a delete; for a
// rename inside one volume this flag is what atomic writers ask for, and it is the
// most a rename can ask, but the documentation does not say more. Unlike os.Rename
// this does not extend the paths with the \\?\ prefix (fixLongPath), so a store
// path of 248 characters or more fails on a Windows without long path support; the
// default folder is far shorter.
func replaceFile(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	var last error
	for attempt := 0; attempt < renameAttempts; attempt++ {
		if last = moveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); last == nil {
			return nil
		}
		if !isBusy(last) {
			break
		}
		// MoveFileEx cannot replace a file that another handle has open, even one
		// opened with delete sharing; a POSIX-semantics rename can.
		if posixReplace(from, to) == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
	return &os.LinkError{Op: "rename", Old: from, New: to, Err: last}
}

// renameAttempts bounds the wait for a reader that has the target open without
// delete sharing (about two seconds in all).
const renameAttempts = 12

func isBusy(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

// posixReplace renames from over to with POSIX semantics (FileRenameInfoEx,
// Windows 10 1809 and later on NTFS): the target's name is released at once even
// while another handle has the old file open. The data is flushed before the
// rename, as the write-through flag would have it.
func posixReplace(from, to string) error {
	abs, err := filepath.Abs(to)
	if err != nil {
		return err
	}
	srcName, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(srcName, windows.GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.FlushFileBuffers(h); err != nil {
		return err
	}
	name, err := windows.UTF16FromString(abs)
	if err != nil {
		return err
	}
	name = name[:len(name)-1] // the length excludes the terminating NUL
	// FILE_RENAME_INFO: Flags (padded to a pointer), RootDirectory (a pointer),
	// FileNameLength (4 bytes), FileName.
	ptr := int(unsafe.Sizeof(uintptr(0)))
	nameOff := 2*ptr + 4
	buf := make([]byte, nameOff+len(name)*2+2)
	*(*uint32)(unsafe.Pointer(&buf[0])) = 0x1 | 0x2 // FILE_RENAME_FLAG_REPLACE_IF_EXISTS | FILE_RENAME_FLAG_POSIX_SEMANTICS
	*(*uint32)(unsafe.Pointer(&buf[2*ptr])) = uint32(len(name) * 2)
	for i, c := range name {
		buf[nameOff+2*i] = byte(c)
		buf[nameOff+2*i+1] = byte(c >> 8)
	}
	return windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buf[0], uint32(len(buf)))
}
