//go:build !windows

package credfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckOwnerOnlyUnixMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cred")
	if err := os.WriteFile(path, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		mode   os.FileMode
		secure bool
	}{{0o600, true}, {0o400, true}, {0o640, false}, {0o604, false}, {0o644, false}} {
		if err := os.Chmod(path, c.mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		err = CheckOwnerOnly(path, info)
		if c.secure && err != nil {
			t.Errorf("mode %o: want allowed, got %v", c.mode, err)
		}
		if !c.secure {
			if !errors.Is(err, ErrInsecure) || err.Error() != "must be mode 0600" {
				t.Errorf("mode %o: want ErrInsecure \"must be mode 0600\", got %v", c.mode, err)
			}
		}
	}
}
