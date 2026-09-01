package secrets

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
)

// fileKeyringEnv is the explicit opt-in for the file-based KEK fallback.
// Without it, a host whose OS keyring is unavailable fails closed exactly as
// before this fallback existed; with it set to "1", the KEK lives in a
// 0600 file under the vault directory instead of the OS keychain.
const fileKeyringEnv = "MONOAGENT_ALLOW_FILE_KEYRING"

// fileKeyringFilename is the KEK file name inside the vault dir
// (~/.monoagent/vault/): dot-prefixed because it is internal vault
// machinery, not a user-facing artifact.
const fileKeyringFilename = ".file-keyring-"

// fileKeyringWarn is printed to stderr on EVERY use of the file-based KEK
// (read or create): the fallback is weaker than the OS keychain and must
// never slip in silently.
const fileKeyringWarn = "file-based keyring fallback in use (MONOAGENT_ALLOW_FILE_KEYRING=1) — weaker than OS keychain; ensure the file is protected (disk encryption, correct permissions)"

// fileKeyringWarnWriter is where warnFileKeyring writes; a variable so tests
// can capture the warnings instead of leaking them into test output.
var fileKeyringWarnWriter io.Writer = os.Stderr

// fileKEKFormat/fileKEKVersion mark the passphrase-protected envelope that
// wraps the on-disk file-based KEK (see fileKEKEnvelope below). A file
// lacking this marker (exactly 32 raw bytes) is the pre-hardening format —
// see migrateLegacyFileKEK.
const (
	fileKEKFormat  = "monoagent-file-keyring"
	fileKEKVersion = 1

	// fileKEKSaltSize matches exportSaltSize (export.go) — both derive a key
	// from a user passphrase with the same argon2id tuning.
	fileKEKSaltSize = 16
)

// fileKEKEnvelope is the on-disk JSON container for the file-based KEK,
// mirroring exportEnvelope's (export.go) shape: the raw 32-byte KEK is never
// written to disk directly. Instead it is AES-256-GCM sealed under a key
// derived from an operator-supplied passphrase via argon2id, using the same
// tuning (argon2Time/argon2Memory/argon2Threads/argon2KeyLen, export.go) the
// vault export/import format already uses. Without the passphrase, reading
// this file yields only ciphertext — the file alone (e.g. leaked via a
// backup or a misconfigured volume) is no longer sufficient to decrypt the
// vault, unlike the raw-KEK format it replaces.
type fileKEKEnvelope struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// filePassphraseFunc supplies the passphrase used to wrap/unwrap the
// file-based KEK. A package-level variable (rather than a direct call)
// purely so tests can substitute a stub, mirroring the fetchKEK /
// keyringGet / keyringSet pattern in keyring.go. The default implementation
// prompts on stderr and reads a line from stdin — the same anti-argv
// pattern readSecretValue (cmd/monoagentcli/secret.go) and the vault
// export/import passphrase prompts (cmd/monoagentcli/secret_export.go) use:
// a passphrase must never be accepted as a CLI flag or environment
// variable, since both leak through shell history and process listings.
var filePassphraseFunc = promptFilePassphrase

func promptFilePassphrase() (string, error) {
	fmt.Fprint(os.Stderr, "File-keyring passphrase: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("secrets: reading file-keyring passphrase: %w", err)
	}
	pass := strings.TrimRight(line, "\r\n")
	if pass == "" {
		return "", errors.New("secrets: empty file-keyring passphrase")
	}
	return pass, nil
}

// wrapFileKEK seals the raw KEK under a key derived from passphrase via
// argon2id, using a fresh random salt and nonce, and returns the resulting
// fileKEKEnvelope as JSON ready to write to disk.
func wrapFileKEK(kek []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, fileKEKSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("secrets: generating file-keyring salt: %w", err)
	}
	derived := argon2.IDKey([]byte(passphrase), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	ciphertext, nonce, err := Encrypt(derived, kek)
	if err != nil {
		return nil, fmt.Errorf("secrets: wrapping file-keyring KEK: %w", err)
	}
	env := fileKEKEnvelope{
		Format: fileKEKFormat, Version: fileKEKVersion, KDF: "argon2id",
		Salt: salt, Nonce: nonce, Ciphertext: ciphertext,
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("secrets: marshaling file-keyring envelope: %w", err)
	}
	return out, nil
}

// unwrapFileKEK reverses wrapFileKEK: derives the same key from passphrase
// and the envelope's stored salt, and opens the sealed KEK.
func unwrapFileKEK(env fileKEKEnvelope, passphrase string) ([]byte, error) {
	derived := argon2.IDKey([]byte(passphrase), env.Salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	kek, err := Decrypt(derived, env.Ciphertext, env.Nonce)
	if err != nil {
		return nil, errors.New("secrets: incorrect file-keyring passphrase or corrupted file")
	}
	if len(kek) != 32 {
		return nil, fmt.Errorf("secrets: unwrapped file-based KEK is %d bytes, want 32", len(kek))
	}
	return kek, nil
}

func fileKeyringEnabled() bool {
	return os.Getenv(fileKeyringEnv) == "1"
}

func fileKeyringPath(profileID string) string {
	return filepath.Join(defaultVaultDir(), fileKeyringFilename+profileID)
}

// defaultVaultDir is the base vault directory without a DB handle — the
// file-based KEK fallback always lives under the default root (~/.monoagent),
// while per-profile KEKs are isolated by the filename suffix.
func defaultVaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".monoagent", "vault")
	}
	return filepath.Join(home, ".monoagent", "vault")
}

func warnFileKeyring() {
	fmt.Fprintf(fileKeyringWarnWriter, "WARN: %s\n", fileKeyringWarn)
}

// readFileKeyringKEK reads the file-based KEK without creating it. A missing
// file is reported as found=false, not an error — the same contract peekKEK
// has for a missing keychain entry. The file is expected to hold a
// passphrase-wrapped fileKEKEnvelope (see wrapFileKEK); a file that isn't
// valid JSON but is exactly 32 bytes is the pre-hardening raw-KEK format and
// is transparently migrated in place — see migrateLegacyFileKEK.
func readFileKeyringKEK(profileID string) (kek []byte, found bool, err error) {
	data, err := os.ReadFile(fileKeyringPath(profileID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("secrets: reading file-based KEK: %w", err)
	}

	var env fileKEKEnvelope
	if jsonErr := json.Unmarshal(data, &env); jsonErr != nil {
		if len(data) == 32 {
			return migrateLegacyFileKEK(profileID, data)
		}
		return nil, false, fmt.Errorf("secrets: file-based KEK %s is not a recognized format (%d bytes): %w", fileKeyringPath(profileID), len(data), jsonErr)
	}
	if env.Format != fileKEKFormat || env.Version != fileKEKVersion || env.KDF != "argon2id" {
		return nil, false, fmt.Errorf("secrets: file-based KEK %s has unsupported envelope format %q/v%d/%q", fileKeyringPath(profileID), env.Format, env.Version, env.KDF)
	}

	passphrase, err := filePassphraseFunc()
	if err != nil {
		return nil, false, err
	}
	kek, err = unwrapFileKEK(env, passphrase)
	if err != nil {
		return nil, false, err
	}
	return kek, true, nil
}

// migrateLegacyFileKEK upgrades a pre-hardening raw-KEK file (32 plaintext
// bytes) to the passphrase-wrapped envelope format in place: it prompts for
// a new passphrase, wraps the SAME KEK bytes under it, and overwrites the
// file. The underlying KEK value never changes, so every vault_keys row
// already wrapped under it keeps decrypting correctly — only how the KEK
// itself is stored at rest changes. If the migration write fails, the
// original raw KEK is still returned so this call itself succeeds (the
// operator can retry the migration on the next invocation); only the
// upgrade is best-effort, not the current operation.
func migrateLegacyFileKEK(profileID string, rawKEK []byte) (kek []byte, found bool, err error) {
	fmt.Fprintf(fileKeyringWarnWriter, "WARN: file-based KEK for profile %q is in the unprotected legacy format — migrating to a passphrase-protected format now\n", profileID)
	passphrase, err := filePassphraseFunc()
	if err != nil {
		return nil, false, fmt.Errorf("secrets: migrating file-based KEK: %w", err)
	}
	wrapped, err := wrapFileKEK(rawKEK, passphrase)
	if err != nil {
		return nil, false, fmt.Errorf("secrets: migrating file-based KEK: %w", err)
	}
	if err := os.WriteFile(fileKeyringPath(profileID), wrapped, 0600); err != nil {
		fmt.Fprintf(fileKeyringWarnWriter, "WARN: failed to persist migrated file-based KEK for profile %q, will retry next use: %v\n", profileID, err)
	}
	return rawKEK, true, nil
}

// peekFileKEK is the file-based counterpart of peekKEK (keyring.go), used by
// fetchOrCreateDEK's lockless fast path when the OS keyring is unavailable
// and the fallback is enabled. Warns whenever an existing KEK file is
// actually used.
func peekFileKEK(profileID string) (kek []byte, found bool, err error) {
	kek, found, err = readFileKeyringKEK(profileID)
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
func fetchOrCreateFileKEK(profileID string) ([]byte, error) {
	warnFileKeyring()

	if err := os.MkdirAll(defaultVaultDir(), 0o700); err != nil {
		return nil, fmt.Errorf("secrets: creating vault dir for file-based KEK: %w", err)
	}

	if kek, found, err := readFileKeyringKEK(profileID); err != nil {
		return nil, err
	} else if found {
		return kek, nil
	}

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		return nil, fmt.Errorf("secrets: generating file-based KEK: %w", err)
	}
	passphrase, err := filePassphraseFunc()
	if err != nil {
		return nil, fmt.Errorf("secrets: creating file-based KEK: %w", err)
	}
	wrapped, err := wrapFileKEK(kek, passphrase)
	if err != nil {
		return nil, fmt.Errorf("secrets: creating file-based KEK: %w", err)
	}

	f, err := os.OpenFile(fileKeyringPath(profileID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// Another process won the create race; adopt its key so both
			// processes wrap DEKs under the same KEK.
			winner, found, rerr := readFileKeyringKEK(profileID)
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
	if _, err := f.Write(wrapped); err != nil {
		f.Close()
		os.Remove(fileKeyringPath(profileID))
		return nil, fmt.Errorf("secrets: writing file-based KEK: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(fileKeyringPath(profileID))
		return nil, fmt.Errorf("secrets: writing file-based KEK: %w", err)
	}
	return kek, nil
}
