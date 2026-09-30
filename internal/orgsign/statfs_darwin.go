//go:build darwin

package orgsign

import "syscall"

func statfsType(path string) string {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return ""
	}
	b := make([]byte, 0, len(st.Fstypename))
	for _, c := range st.Fstypename {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b) // nfs, smbfs, msdos, exfat, macfuse, ...
}

func platformUntrusted() string { return "" }
