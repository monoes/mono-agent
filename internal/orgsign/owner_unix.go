//go:build !windows

package orgsign

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// foreignOwner reports a file owned by another uid.
func foreignOwner(st fs.FileInfo, what string) string {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	if uid := os.Getuid(); int(sys.Uid) != uid {
		return fmt.Sprintf("%s is owned by uid %d, not %d", what, sys.Uid, uid)
	}
	return ""
}

// looseMode reports group/other permission bits.
func looseMode(st fs.FileInfo) bool { return st.Mode().Perm()&0o077 != 0 }

// multiplyLinked reports a file with other hard links, which monomind
// refuses to read as an instructions file.
func multiplyLinked(st fs.FileInfo) bool {
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && sys.Nlink > 1
}
