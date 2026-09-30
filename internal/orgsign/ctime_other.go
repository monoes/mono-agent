//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package orgsign

import "syscall"

// ctimeNs is 0 where the stat layout isn't known here; inode, size and
// mtime still change on a swap.
func ctimeNs(*syscall.Stat_t) int64 { return 0 }
