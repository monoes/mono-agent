//go:build !windows

package account

import "os"

// syncDir flushes a directory's entries to disk, so that a rename or a remove in
// it survives a power cut: an fsync of the file alone does not make its
// directory entry durable. It is best effort and ignores every error: some
// filesystems refuse to sync a directory (EINVAL), and a write that has already
// succeeded must not fail because of it.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
