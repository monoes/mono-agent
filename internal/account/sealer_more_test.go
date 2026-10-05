package account

import (
	"bytes"
	"context"
	"crypto/aes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/secrets"
)

func fixedKeySealer(key []byte) keyringSealer {
	return keyringSealer{kek: func(bool) ([]byte, bool, error) { return key, true, nil }}
}

// Sealing may create the key (the first sign-in). Opening only reads it: an
// open that created a key would hide a lost key behind a fresh one.
func TestSealAsksForTheKeyToBeCreatedAndOpenOnlyReadsIt(t *testing.T) {
	var asked []bool
	key := bytes.Repeat([]byte{7}, 32)
	s := keyringSealer{kek: func(create bool) ([]byte, bool, error) {
		asked = append(asked, create)
		return key, true, nil
	}}
	sealed, err := s.Seal([]byte("refresh-value-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(sealed); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || !asked[0] || asked[1] {
		t.Fatalf("create flags = %v, want [true false] (Seal, then Open)", asked)
	}
}

// refresh.enc outlives the binary that wrote it, so the layout is checked
// against the primitive, in both directions, and not only against open().
func TestSealedLayoutIsVersionNonceCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plain := []byte("refresh-value-1")
	s := fixedKeySealer(key)
	blob, err := s.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if blob[0] != 1 {
		t.Errorf("version byte = %d, want 1", blob[0])
	}
	if want := 1 + 12 + len(plain) + 16; len(blob) != want {
		t.Errorf("len = %d, want %d (version, 12-byte nonce, ciphertext, 16-byte tag)", len(blob), want)
	}
	if got, err := secrets.Decrypt(key, blob[13:], blob[1:13]); err != nil || !bytes.Equal(got, plain) {
		t.Errorf("the ciphertext after the 13-byte head does not open with the nonce before it (match=%v, err=%v)", bytes.Equal(got, plain), err)
	}

	ciphertext, nonce, err := secrets.Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	byHand := append(append([]byte{1}, nonce...), ciphertext...)
	if got, err := s.Open(byHand); err != nil || !bytes.Equal(got, plain) {
		t.Errorf("a blob built by hand from the primitive does not open (match=%v, err=%v)", bytes.Equal(got, plain), err)
	}
}

// A damaged blob is refused cleanly at every length, never a slice panic, and a
// version byte other than 1 is refused whether it is older or newer.
func TestSealedBlobsOfEveryLengthAndVersionAreRefused(t *testing.T) {
	s := NewMemorySealer()
	sealed, err := s.Seal([]byte("refresh-value-1"))
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(sealed); n++ {
		if _, err := s.Open(sealed[:n]); !errors.Is(err, ErrKeyringUnavailable) {
			t.Errorf("the first %d bytes: err = %v, want ErrKeyringUnavailable", n, err)
		}
	}
	for _, version := range []byte{0, 2, 255} {
		blob := append([]byte(nil), sealed...)
		blob[0] = version
		if _, err := s.Open(blob); !errors.Is(err, ErrKeyringUnavailable) {
			t.Errorf("version byte %d: err = %v, want ErrKeyringUnavailable", version, err)
		}
	}
}

// Every failure wraps ErrKeyringUnavailable and keeps its cause in the chain,
// at each place it can arise: the key store, the cipher on Seal and on Open.
func TestSealerErrorsKeepTheirCause(t *testing.T) {
	plain := []byte("refresh-value-1")
	broken := errors.New("keychain locked")
	failing := keyringSealer{kek: func(bool) ([]byte, bool, error) { return nil, false, broken }}
	if _, err := failing.Seal(plain); !errors.Is(err, ErrKeyringUnavailable) || !errors.Is(err, broken) {
		t.Errorf("Seal with a failing key store: err = %v, want ErrKeyringUnavailable wrapping the cause", err)
	}

	sealed, err := fixedKeySealer(bytes.Repeat([]byte{7}, 32)).Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	shortKey := fixedKeySealer([]byte("short"))
	var size aes.KeySizeError
	if _, err := shortKey.Seal(plain); !errors.Is(err, ErrKeyringUnavailable) || !errors.As(err, &size) {
		t.Errorf("Seal with a key of bad length: err = %v, want ErrKeyringUnavailable wrapping the cipher's error", err)
	}
	if _, err := shortKey.Open(sealed); !errors.Is(err, ErrKeyringUnavailable) || !errors.As(err, &size) {
		t.Errorf("Open with a key of bad length: err = %v, want ErrKeyringUnavailable wrapping the cipher's error", err)
	}
}

// sealerFreshProcessEnv marks the child process of TestKeyringSealersInAFreshProcess.
const sealerFreshProcessEnv = "ACCOUNT_SEALER_FRESH_PROCESS"

// The production sealers keep process-wide state: secrets remembers an account
// key it created, and the key store is the one in-memory keyring TestMain
// installs. The checks that create a key therefore run in a child process of
// their own (the idiom daemonhb's lock test uses) and leave nothing behind for
// TestKeyringSealersOverTheMockKeyring or any other test of this package. What
// they pin: the quiet sealer never makes a key, the interactive one may, and an
// Open never makes one. A child that matches no test would pass without running
// anything, so the parent also requires the child's own PASS line.
func TestKeyringSealersInAFreshProcess(t *testing.T) {
	if os.Getenv(sealerFreshProcessEnv) == "1" {
		sealerFreshProcessChecks(t)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestKeyringSealersInAFreshProcess$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), sealerFreshProcessEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the fresh-process checks failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestKeyringSealersInAFreshProcess") {
		t.Fatalf("the child process ran none of the checks:\n%s", out)
	}
}

func sealerFreshProcessChecks(t *testing.T) {
	plain := []byte("refresh-value-1")
	blob, err := NewMemorySealer().Seal(plain) // well formed; which key sealed it does not matter here
	if err != nil {
		t.Fatal(err)
	}
	isolate := func(t *testing.T, fileKeyring string) (home string) {
		t.Helper()
		home = t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("MONOAGENT_ALLOW_FILE_KEYRING", fileKeyring)
		t.Setenv("MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE", "")
		return home
	}

	t.Run("opening with no key reports it missing and creates nothing", func(t *testing.T) {
		isolate(t, "")
		for name, s := range map[string]Sealer{"quiet": NewKeyringSealer(), "interactive": NewInteractiveKeyringSealer()} {
			_, err := s.Open(blob)
			if !errors.Is(err, ErrKeyringUnavailable) || !strings.Contains(err.Error(), "missing") {
				t.Errorf("%s: err = %v, want ErrKeyringUnavailable saying the key is missing", name, err)
			}
			if _, found, err := s.(keyringSealer).kek(false); found || err != nil {
				t.Errorf("%s: Open left a key behind (found=%v, err=%v)", name, found, err)
			}
		}
	})

	t.Run("only the interactive sealer may make the file keyring", func(t *testing.T) {
		home := isolate(t, "1")
		passphrase := filepath.Join(t.TempDir(), "passphrase")
		if err := os.WriteFile(passphrase, []byte("a passphrase for this test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE", passphrase) // so nothing here can reach a prompt
		vault := filepath.Join(home, ".monoagent", "vault")

		if _, err := NewKeyringSealer().Seal(plain); !errors.Is(err, ErrKeyringUnavailable) {
			t.Fatalf("the quiet sealer made a key: err = %v, want ErrKeyringUnavailable", err)
		}
		if _, err := os.Stat(vault); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the quiet sealer wrote under %s: %v", vault, err)
		}

		sealed, err := NewInteractiveKeyringSealer().Seal(plain)
		if err != nil {
			t.Fatalf("the interactive sealer could not make the file keyring: %v", err)
		}
		if entries, err := os.ReadDir(vault); err != nil || len(entries) != 1 {
			t.Fatalf("the vault holds %d entries (err %v), want the one file keyring", len(entries), err)
		}

		for name, s := range map[string]Sealer{"quiet": NewKeyringSealer(), "interactive": NewInteractiveKeyringSealer()} {
			if got, err := s.Open(sealed); err != nil || !bytes.Equal(got, plain) {
				t.Errorf("%s: Open failed (match=%v, err=%v)", name, bytes.Equal(got, plain), err)
			}
		}
		// A refresh seals the rotated token with the key that exists: a quiet Seal
		// needs no prompt once the file keyring is there.
		if _, err := NewKeyringSealer().Seal(plain); err != nil {
			t.Errorf("a quiet Seal with the file keyring in place failed: %v", err)
		}
	})
}
