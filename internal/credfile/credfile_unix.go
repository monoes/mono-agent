//go:build !windows

package credfile

import "os"

func checkOwnerOnly(_ string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return &insecureError{"must be mode 0600"}
	}
	return nil
}
