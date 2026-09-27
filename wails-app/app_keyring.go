package main

// Vault keyring settings (Settings page → "Vault keyring", shown only on
// hosts with no OS keychain). Every method shells out to `monoagentcli
// secret keyring …` through runMonoCLI; the passphrase only travels on the
// CLI's stdin — never argv, never a log line.

import (
	"errors"
	"strings"
)

// KeyringPassphraseFile mirrors `secret keyring status`'s passphrase_file.
type KeyringPassphraseFile struct {
	Source     string `json:"source"` // env | configured | none
	Configured bool   `json:"configured"`
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Mode       string `json:"mode"`
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
}

// KeyringStatusInfo mirrors `secret keyring status --json`.
type KeyringStatusInfo struct {
	Backend            string                `json:"backend"` // os | file | unavailable
	OSKeyringError     string                `json:"os_keyring_error"`
	FileKeyringAllowed bool                  `json:"file_keyring_allowed"`
	FileKeyrings       []string              `json:"file_keyrings"`
	PassphraseFile     KeyringPassphraseFile `json:"passphrase_file"`
}

// KeyringPassphraseResult mirrors `secret keyring set-passphrase|clear-passphrase --json`.
type KeyringPassphraseResult struct {
	Path    string `json:"path"`
	Saved   bool   `json:"saved"`
	Removed bool   `json:"removed"`
}

// KeyringStatus reports the vault's key store (never prompts).
func (a *App) KeyringStatus() (KeyringStatusInfo, error) {
	var st KeyringStatusInfo
	if err := a.runMonoCLI("", &st, "secret", "keyring", "status"); err != nil {
		return KeyringStatusInfo{}, err
	}
	if st.FileKeyrings == nil {
		st.FileKeyrings = []string{}
	}
	return st, nil
}

// KeyringSetPassphrase saves the file-keyring passphrase. The CLI checks it
// unlocks an existing file keyring before saving. Passed only on stdin.
func (a *App) KeyringSetPassphrase(passphrase string) (KeyringPassphraseResult, error) {
	passphrase = strings.TrimRight(passphrase, "\r\n")
	if passphrase == "" {
		return KeyringPassphraseResult{}, errors.New("enter the passphrase first")
	}
	if strings.ContainsAny(passphrase, "\r\n") {
		return KeyringPassphraseResult{}, errors.New("the passphrase must be a single line")
	}
	var res KeyringPassphraseResult
	if err := a.runMonoCLI(passphrase+"\n", &res, "secret", "keyring", "set-passphrase"); err != nil {
		return KeyringPassphraseResult{}, err
	}
	return res, nil
}

// KeyringClearPassphrase deletes the saved passphrase file.
func (a *App) KeyringClearPassphrase() (KeyringPassphraseResult, error) {
	var res KeyringPassphraseResult
	if err := a.runMonoCLI("", &res, "secret", "keyring", "clear-passphrase"); err != nil {
		return KeyringPassphraseResult{}, err
	}
	return res, nil
}
