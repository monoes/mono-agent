//go:build linux || openbsd || dragonfly

package orgsign

import "syscall"

func ctimeNs(st *syscall.Stat_t) int64 { return int64(st.Ctim.Sec)*1e9 + int64(st.Ctim.Nsec) }
