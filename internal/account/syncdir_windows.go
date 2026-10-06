//go:build windows

package account

// syncDir does nothing on Windows: it has no way to flush a directory's entries,
// and NTFS journals the rename or the remove itself.
func syncDir(string) {}
