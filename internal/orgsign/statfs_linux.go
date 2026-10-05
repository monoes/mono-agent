//go:build linux

package orgsign

import "syscall"

// linuxFSNames are the statfs f_type magics of the untrusted filesystems.
var linuxFSNames = map[int64]string{
	0x6969:     "nfs",
	0x517B:     "smb",
	0xFE534D42: "smb2",
	0xFF534D42: "cifs",
	0x65735546: "fuse",
	0x4d44:     "vfat",
	0x2011BAB0: "exfat",
}

func statfsType(path string) string {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return ""
	}
	return linuxFSNames[int64(uint32(st.Type))] // f_type is 32 bits wide on some arches
}

func platformUntrusted() string { return "" }
