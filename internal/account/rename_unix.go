//go:build !windows

package account

import "os"

// replaceFile renames from over to. On Unix a rename over an existing file is
// atomic, and writeFileAtomic flushes the directory after it (syncDir), which
// makes it durable.
func replaceFile(from, to string) error { return os.Rename(from, to) }
