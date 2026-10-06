//go:build windows

package account

import (
	"os"

	"golang.org/x/sys/windows"
)

// moveFileEx is windows.MoveFileEx, a variable so that a test can see the flags
// it is given.
var moveFileEx = windows.MoveFileEx

// replaceFile renames from over to and returns only once the change is on the
// disk (MOVEFILE_WRITE_THROUGH). Windows has no directory flush (syncDir does
// nothing there), and os.Rename passes MOVEFILE_REPLACE_EXISTING alone: a power
// cut just after a refresh could then bring back the old refresh.enc, whose token
// monoes.me has rotated away, or a session.json without a record that had to be
// on the disk before the grant was sent, and the next start would present a dead
// token.
func replaceFile(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	if err := moveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
