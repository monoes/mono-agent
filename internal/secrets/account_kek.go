package secrets

import (
	"bytes"
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
//     Without the file keyring the key is made in the OS keychain. With the
//     file keyring opted in (MONOAGENT_ALLOW_FILE_KEYRING=1) it is made in the
//     file keyring, which needs its passphrase, so only an interactive call
//     can make it.
//   - interactive=true may ask for the file keyring's passphrase, exactly as
//     the vault does. Only an explicit command that owns the terminal (the
//     sign-in) should pass it.
//   - interactive=false never asks for the file keyring's passphrase (no
//     prompt, no read of stdin or of /dev/tty), so a gate that runs before a
//     command has claimed stdin cannot swallow the command's piped input as a
//     passphrase. That is all it promises: like any vault call it can still
//     wait inside the OS keychain backend (a locked macOS keychain, a Secret
//     Service unlock dialog, the first-use write), which is why callers touch
//     the key only when a refresh is due (spec D16 and §4.6, issue #54).
//     Without the file keyring there is no passphrase to ask for, so it
//     behaves as interactive=true. With the file keyring opted in it reads the
//     key only when the passphrase is already known (remembered in this
//     process, or in MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE or the configured
//     passphrase file) and it never creates a key: creating the file keyring
//     needs the passphrase prompt that the interactive sign-in owns, so
//     create=true returns the key that exists (in the OS keychain or the file
//     keyring) or an error.
//
// Every call reads the key store afresh, a creating one included: nothing is
// remembered between calls, so a key that was replaced in the key store while the
// process runs is the key the next call returns. A key store that cannot be
// opened is an error, not found=false. The key is a copy the caller may wipe.
func AccountKEK(create, interactive bool) (kek []byte, found bool, err error) {
	if interactive || !fileKeyringEnabled() {
		// Without the file keyring there is no passphrase to ask for, so one path
		// serves both. (The OS keychain backend may still wait on a dialog of its
		// own, in either mode.)
		kek, found, err = accountKEKVault(create)
	} else {
		kek, found, err = accountKEKQuiet(create)
	}
	// The caller gets a copy of its own, so wiping it cannot change the key any
	// other call gets.
	return bytes.Clone(kek), found, err
}

// accountKEKVault reads or creates the key through the vault's own functions.
// It creates through fetchOrCreateKEK, not the vault's memoized getOrCreateKEK:
// the key store's entry can be replaced while a process runs (a keychain reset,
// then a sign-in in another process that makes a new key), and a remembered key
// would seal the next refresh token under a key no process, this one included,
// can find again. fetchOrCreateKEK reads first and makes a key only when the OS
// keychain answers that it has no entry: a key store error is returned and
// writes nothing. Under the file keyring opt-in it goes to the file keyring
// instead, as it does for the vault. Within a process keyringIOMu makes its read
// and its write one step; across processes the account's callers make every
// creating call under session.lock, and the file keyring creates with O_EXCL.
func accountKEKVault(create bool) ([]byte, bool, error) {
	if create {
		kek, err := fetchOrCreateKEK(accountKEKID)
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
		return nil, false, errors.New("secrets: creating the file-keyring key needs its passphrase prompt, which this call must not show; the interactive sign-in creates it (monoagentcli account login)")
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
