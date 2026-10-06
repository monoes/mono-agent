//go:build windows

package account

// syncDir does nothing on Windows, which has no directory flush. A write closes
// the power-cut window another way there: its rename is written through
// (replaceFile, MOVEFILE_WRITE_THROUGH). A remove (DeleteRefresh) is not: that
// window stays open on Windows.
func syncDir(string) {}
