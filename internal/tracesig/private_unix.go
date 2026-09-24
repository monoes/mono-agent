//go:build !windows

package tracesig

import (
	"fmt"
	"os"
)

// checkPrivate refuses a key file other users can read or write, as ssh
// refuses a private key: anyone who can read it can sign traces.
func checkPrivate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s is readable or writable by other users (mode %04o); run: chmod 600 %s", path, perm, path)
	}
	return nil
}
