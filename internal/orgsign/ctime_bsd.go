//go:build darwin || freebsd || netbsd

package orgsign

import "syscall"

func ctimeNs(st *syscall.Stat_t) int64 { return int64(st.Ctimespec.Sec)*1e9 + int64(st.Ctimespec.Nsec) }
