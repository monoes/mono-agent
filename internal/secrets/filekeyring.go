package secrets

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/monoes/mono-agent/internal/vault"
)

// fileKeyringEnv is the explicit opt-in for the file-based KEK fallback.
// Without it, a host whose OS keyring is unavailable fails closed exactly as
// before this fallback existed; with it set to "1", the KEK lives in a
// 0600 file under the vault directory instead of the OS keychain.
const fileKeyringEnv = "MONOAGENT_ALLOW_FILE_KEYRING"

// fileKeyringFilename is the KEK file name inside the vault dir
// (~/.monoagent/vault/): dot-prefixed because it is internal vault
// machinery, not a user-facing artifact.
const fileKeyringFilename = ".file-keyring"

// fileKeyringWarn is printed to stderr on EVERY use of the file-based KEK
// (read or create): the fallback is weaker than the OS keychain and must
// never slip in silently.
const fileKeyringWarn = "file-based keyring fallback in use (MONOAGENT_ALLOW_FILE_KEYRING=1) — weaker than OS keychain; ensure the file is protected (disk encryption, correct permissions)"

// fileKeyringWarnWriter is where warnFileKeyring writes; a variable so tests
// can capture the warnings instead of leaking them into test output.
var fileKeyringWarnWriter io.Writer = os.Stderr

func fileKeyringEnabled() bool {
	return os.Getenv(fileKeyringEnv) == "1"
}

func fileKeyringPath() string {
	return filepath.Join(vault.VaultDir(), fileKeyringFilename)
}

func warnFileKeyring() {
	fmt.Fprintf(fileKeyringWarnWriter, "WARN: %s\n", fileKeyringWarn)
}

// readFileKeyringKEK reads the file-based KEK without creating it. A missing
// file is reported as found=false, not an error — the same contract peekKEK
// has for a missing keychain entry.
func readFileKeyringKEK() (kek []byte, found bool, err error) {
	kek, err = os.ReadFile(fileKeyringPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("secrets: reading file-based KEK: %w", err)
	}
	if len(kek) != 32 {
		return nil, false, fmt.Errorf("secrets: file-based KEK %s is %d bytes, want 32", fileKeyringPath(), len(kek))
	}
	return kek, true, nil
}

// peekFileKEK is the file-based counterpart of peekKEK (keyring.go), used by
// fetchOrCreateDEK's lockless fast path when the OS keyring is unavailable
// and the fallback is enabled. Warns whenever an existing KEK file is
// actually used.
func peekFileKEK() (kek []byte, found bool, err error) {
	kek, found, err = readFileKeyringKEK()
	if err != nil || !found {
		return nil, found, err
	}
	warnFileKeyring()
	return kek, true, nil
}

// fetchOrCreateFileKEK is the file-based counterpart of fetchOrCreateKEK
// (keyring.go): return the KEK from ~/.monoagent/vault/.file-keyring,
// generating the 32 random bytes and creating the file (mode 0600, dir 0700)
// exactly once on first use. Warns on every use, creation included.
//
// Called only from peekKEK/fetchOrCreateKEK's fallback branches, i.e. always
// under keyringIOMu (in-process serialization). Cross-process creation races
// are settled by O_EXCL: the loser of the create re-reads the winner's file.
// Real callers reach creation via bootstrapDEKLocked's BEGIN IMMEDIATE,
// which already serializes first-time bootstrap across processes.
func fetchOrCreateFileKEK() ([]byte, error) {
	warnFileKeyring()

	if err := vault.EnsureVaultDir(); err != nil {
		return nil, fmt.Errorf("secrets: creating vault dir for file-based KEK: %w", err)
	}

	if kek, found, err := readFileKeyringKEK(); err != nil {
		return nil, err
	} else if found {
		return kek, nil
	}

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		return nil, fmt.Errorf("secrets: generating file-based KEK: %w", err)
	}
	f, err := os.OpenFile(fileKeyringPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// Another process won the create race; adopt its key so both
			// processes wrap DEKs under the same KEK.
			winner, found, rerr := readFileKeyringKEK()
			if rerr != nil {
				return nil, rerr
			}
			if !found {
				return nil, fmt.Errorf("secrets: file-based KEK vanished after create race: %w", err)
			}
			return winner, nil
		}
		return nil, fmt.Errorf("secrets: creating file-based KEK: %w", err)
	}
	if _, err := f.Write(kek); err != nil {
		f.Close()
		os.Remove(fileKeyringPath())
		return nil, fmt.Errorf("secrets: writing file-based KEK: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(fileKeyringPath())
		return nil, fmt.Errorf("secrets: writing file-based KEK: %w", err)
	}
	return kek, nil
}
