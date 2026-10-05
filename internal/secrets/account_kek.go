package secrets

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// accountKEKID names the key that seals the monoes.me account's refresh token
// (internal/account) in the vault's own key stores: the OS keychain entry
// "kek-<id>" under the vault's service, or the file keyring's
// ".file-keyring-<id>". It contains ".." so no real profile can own it
// (profiledir.ValidProfileID rejects that), and so it reads as "not a
// profile" in `secret keyring status`.
const accountKEKID = "monoes..account"

// AccountKEK returns the 32-byte key that seals the monoes.me account's
// refresh token, from the same key stores and with the same fallbacks the
// vault uses for a profile's KEK.
//
//   - create=false only reads: found=false means there is no key yet, and
//     nothing is written to the keychain or to disk.
//   - create=true generates the key when it is missing (the first sign-in).
//   - interactive=true may ask for the file keyring's passphrase, exactly as
//     the vault does. Only an explicit command that owns the terminal (the
//     sign-in) should pass it.
//   - interactive=false never prompts, so a gate that runs before a command
//     has claimed stdin cannot swallow the command's piped input as a
//     passphrase. With the file keyring it reads the key only when the
//     passphrase is already known (remembered in this process, or in
//     MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE or the configured passphrase
//     file) and it creates a key only through the OS keychain.
//
// A key store that cannot be opened is an error, not found=false.
func AccountKEK(create, interactive bool) (kek []byte, found bool, err error) {
	if interactive || !fileKeyringEnabled() {
		// Without the file keyring nothing can prompt, so one path serves both.
		return accountKEKVault(create)
	}
	return accountKEKQuiet(create)
}

// accountKEKVault reads or creates the key through the vault's own functions.
func accountKEKVault(create bool) ([]byte, bool, error) {
	if create {
		kek, err := getOrCreateKEK(accountKEKID)
		return kek, err == nil, err
	}
	kek, found, err := peekKEK(accountKEKID)
	if err != nil || found || !fileKeyringEnabled() {
		return kek, found, err
	}
	// The OS keychain answers but has no entry. With the file keyring opted in
	// the vault's first-use creation (fetchOrCreateKEK) puts the key in the
	// file, so look there before reporting "no key".
	return peekFileKEK(accountKEKID)
}

// accountKEKQuiet is AccountKEK(create, false) with the file keyring opted in.
func accountKEKQuiet(create bool) ([]byte, bool, error) {
	keyringIOMu.Lock()
	stored, err := keyringGet(keyringService, kekAccount(accountKEKID))
	keyringIOMu.Unlock()
	if err == nil {
		key, derr := hex.DecodeString(stored)
		if derr != nil {
			return nil, false, fmt.Errorf("secrets: decoding stored KEK: %w", derr)
		}
		return key, true, nil
	}
	// No entry, or an OS keychain that is unavailable: both lead to the file
	// keyring, as fetchOrCreateKEK routes them.
	kek, found, ferr := readAccountFileKEK()
	if ferr != nil || found {
		return kek, found, ferr
	}
	if create {
		return nil, false, fmt.Errorf("secrets: creating the file-keyring key needs its passphrase prompt, which this call must not show; %s", filePassphraseHint)
	}
	return nil, false, nil
}

// readAccountFileKEK reads the account's file-keyring KEK without creating it
// and without prompting. A missing file is found=false.
func readAccountFileKEK() (kek []byte, found bool, err error) {
	path := fileKeyringPath(accountKEKID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("secrets: reading file-based KEK: %w", err)
	}
	var env fileKEKEnvelope
	if json.Unmarshal(data, &env) != nil || env.Format != fileKEKFormat || env.Version != fileKEKVersion || env.KDF != "argon2id" {
		return nil, false, fmt.Errorf("secrets: file-based KEK %s is not a recognized format", path)
	}
	pass, err := quietFilePassphrase(accountKEKID)
	if err != nil {
		return nil, false, err
	}
	kek, err = unwrapFileKEK(env, pass)
	if err != nil {
		return nil, false, err
	}
	rememberFilePassphrase(accountKEKID, pass)
	warnFileKeyring()
	return kek, true, nil
}

// quietFilePassphrase is filePassphraseFor without its last two sources (stdin
// and the terminal): a passphrase remembered in this process, then
// MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE, then the configured passphrase file.
func quietFilePassphrase(profileID string) (string, error) {
	knownFilePassphrasesMu.Lock()
	pass, ok := knownFilePassphrases[profileID]
	knownFilePassphrasesMu.Unlock()
	if ok {
		return pass, nil
	}
	if path := os.Getenv(filePassphraseFileEnv); path != "" {
		return readPassphraseFile(path)
	}
	if path := ConfiguredPassphrasePath(); fileExists(path) {
		return readPassphraseFileAs(path, "configured passphrase file")
	}
	return "", fmt.Errorf("secrets: the file keyring (MONOAGENT_ALLOW_FILE_KEYRING=1) needs its passphrase and this call must not prompt for it; %s", filePassphraseHint)
}
