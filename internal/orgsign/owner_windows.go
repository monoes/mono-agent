//go:build windows

package orgsign

import "io/fs"

// On Windows monomind checks neither the owner nor the mode bits
// (process.getuid is undefined there), and nlink is not reported.
func foreignOwner(fs.FileInfo, string) string { return "" }

func looseMode(fs.FileInfo) bool { return false }

func multiplyLinked(fs.FileInfo) bool { return false }

// fileID is what Windows reports through os.FileInfo: size, mode and
// modification time (no inode or ctime).
type fileID struct {
	size                   int64
	mtimeNs, ctimeNs, mode int64 // ctimeNs stays 0 here
}

func idOf(st fs.FileInfo) fileID {
	return fileID{size: st.Size(), mtimeNs: st.ModTime().UnixNano(), mode: int64(st.Mode())}
}

// dirIDOf: Windows reports no inode or ctime through os.FileInfo; a
// directory is stamped only as existing.
func dirIDOf(fs.FileInfo) fileID { return fileID{} }
