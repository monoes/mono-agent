package secrets

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const (
	releaseKeyService = "monoagent-release-signing"
	releaseKeyAccount = "ed25519-v1"
)

// ErrNoReleaseKey means no release signing key is stored in the OS keychain.
var ErrNoReleaseKey = errors.New("no release signing key in the OS keychain")

// StoreReleaseSigningKey saves the release signing private key (base64 text)
// in the OS keychain (value: standard base64 of the 64-byte Ed25519
// private key, seed||pub), through the same keyring package vars as the vault.
func StoreReleaseSigningKey(b64 string) error {
	keyringIOMu.Lock()
	defer keyringIOMu.Unlock()
	return keyringSet(releaseKeyService, releaseKeyAccount, b64)
}

// ReleaseSigningKey reads it back; ErrNoReleaseKey when none is stored.
func ReleaseSigningKey() (string, error) {
	keyringIOMu.Lock()
	defer keyringIOMu.Unlock()
	v, err := keyringGet(releaseKeyService, releaseKeyAccount)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNoReleaseKey
		}
		return "", err
	}
	return v, nil
}
