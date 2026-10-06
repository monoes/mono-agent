package account

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/monoes/mono-agent/internal/secrets"
)

// ErrKeyringUnavailable means the key that seals the refresh token cannot be
// used: the key store cannot be opened or does not answer in time
// (keyStoreTimeout), the key is missing, or it does not open the stored token.
// The session stays usable until its grace ends, and `account status` reports
// keyring_unavailable so it is not mistaken for a network problem.
var ErrKeyringUnavailable = errors.New("account: key store unavailable")

// Sealer seals the refresh token under a key from the OS keyring or the
// file-keyring fallback, as the vault does.
type Sealer interface {
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// sealedVersion starts every sealed blob: version byte, 12-byte nonce, then
// the AES-256-GCM ciphertext (the vault's primitive, secrets.Encrypt).
const (
	sealedVersion = 1
	nonceSize     = 12
	keySize       = 32
)

// keyringSealer seals under the account key of internal/secrets. kek is
// secrets.AccountKEK with the interactive choice bound in; it is a field so a
// test can stand in for the key store. mayPrompt marks the sign-in's sealer: it
// may wait for a person, so the store does not bound its calls (callKeyStore).
type keyringSealer struct {
	kek       func(create bool) (key []byte, found bool, err error)
	mayPrompt bool
}

// promptingSealer is implemented by a sealer that may wait for a person: one
// that asks for a passphrase on the terminal or shows an unlock dialog. The store
// does not put keyStoreTimeout on the calls of such a sealer.
type promptingSealer interface{ prompts() bool }

func (s keyringSealer) prompts() bool { return s.mayPrompt }

// isPrompting reports whether s may wait for a person. A sealer that does not say
// so, the memory sealer and a test double included, does not.
func isPrompting(s Sealer) bool {
	p, ok := s.(promptingSealer)
	return ok && p.prompts()
}

// NewKeyringSealer is the production sealer for implicit use: it never prompts
// for the file keyring's passphrase, so a gate that runs before a command has
// claimed stdin cannot swallow the command's piped input. With the file keyring
// the passphrase must come from MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE or the
// configured passphrase file, or the key store counts as unavailable.
func NewKeyringSealer() Sealer {
	return keyringSealer{kek: func(create bool) ([]byte, bool, error) { return secrets.AccountKEK(create, false) }}
}

// NewInteractiveKeyringSealer is NewKeyringSealer for an explicit command that
// owns the terminal (the sign-in): it may ask for the file keyring's
// passphrase, exactly as the vault does. Unlike every other sealer, the store
// does not put keyStoreTimeout on its calls: a person types the passphrase and
// its confirmation, or answers the unlock dialog, and that takes as long as it
// takes. The command owns the terminal, and it is the only one that holds
// session.lock while it waits; a refresh of another process just waits behind it.
func NewInteractiveKeyringSealer() Sealer {
	return keyringSealer{kek: func(create bool) ([]byte, bool, error) { return secrets.AccountKEK(create, true) }, mayPrompt: true}
}

func (s keyringSealer) Seal(plain []byte) ([]byte, error) {
	key, _, err := s.kek(true)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeyringUnavailable, err)
	}
	return seal(key, plain)
}

func (s keyringSealer) Open(sealed []byte) ([]byte, error) {
	key, found, err := s.kek(false)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrKeyringUnavailable, err)
	case !found:
		return nil, fmt.Errorf("%w: the key that sealed the refresh token is missing; sign in again", ErrKeyringUnavailable)
	}
	return open(key, sealed)
}

// memorySealer is the test sealer: a random key that lives in memory.
type memorySealer struct{ key []byte }

// NewMemorySealer returns a sealer with a fresh in-memory key, for tests. Two
// of them cannot open each other's blobs.
func NewMemorySealer() Sealer {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("account: no randomness: " + err.Error())
	}
	return memorySealer{key: key}
}

func (m memorySealer) Seal(plain []byte) ([]byte, error)  { return seal(m.key, plain) }
func (m memorySealer) Open(sealed []byte) ([]byte, error) { return open(m.key, sealed) }

// checkKey refuses a key that is not 32 bytes, naming the length and never the
// key. secrets.Encrypt would also take 16 and 24 bytes and seal under AES-128 or
// AES-192, while the sealed format promises AES-256-GCM.
func checkKey(key []byte) error {
	if len(key) != keySize {
		return fmt.Errorf("%w: the key is %d bytes, want %d", ErrKeyringUnavailable, len(key), keySize)
	}
	return nil
}

func seal(key, plain []byte) ([]byte, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	ciphertext, nonce, err := secrets.Encrypt(key, plain)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeyringUnavailable, err)
	}
	out := make([]byte, 0, 1+len(nonce)+len(ciphertext))
	out = append(out, sealedVersion)
	out = append(out, nonce...)
	return append(out, ciphertext...), nil
}

// open fails with ErrKeyringUnavailable for a key that is not 32 bytes, a damaged
// blob and a blob the key does not open: every way the refresh token cannot be
// read.
func open(key, sealed []byte) ([]byte, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	if len(sealed) < 1+nonceSize || sealed[0] != sealedVersion {
		return nil, fmt.Errorf("%w: the sealed refresh token is damaged or from a newer version", ErrKeyringUnavailable)
	}
	plain, err := secrets.Decrypt(key, sealed[1+nonceSize:], sealed[1:1+nonceSize])
	if err != nil {
		return nil, fmt.Errorf("%w: the stored key does not open the sealed refresh token: %w", ErrKeyringUnavailable, err)
	}
	return plain, nil
}
