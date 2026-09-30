//go:build windows

package orgsign

import "io/fs"

// On Windows monomind checks neither the owner nor the mode bits
// (process.getuid is undefined there), and nlink is not reported.
func foreignOwner(fs.FileInfo, string) string { return "" }

func looseMode(fs.FileInfo) bool { return false }

func multiplyLinked(fs.FileInfo) bool { return false }
