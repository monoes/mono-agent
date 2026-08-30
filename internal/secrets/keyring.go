package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "monoagent-vault"
	// legacyKeyringAccount is the single fixed KEK account every profile
	// shared before per-profile keys existed. Read-only, used only by the
	// vault-key migration (migrate_profile_keys.go) to unwrap existing
	// secrets before re-encrypting them under a fresh per-profile key.
	legacyKeyringAccount = "kek"
)

// kekAccount returns the OS-keychain account name for one profile's KEK.
// Each profile gets its own keychain entry — a profile's vault secrets are
// unrecoverable without both its own vault_keys row (the wrapped DEK) and
// this specific keychain entry (the KEK that unwraps it).
func kekAccount(profileID string) string {
	return "kek-" + profileID
}

// kekAttempt holds the memoized result of one profile's getOrCreateKEK
// attempt, guarded by its own sync.Once so that attempt's fetchKEK call runs
// at most once no matter how many goroutines race on it.
type kekAttempt struct {
	once sync.Once
	kek  []byte
	err  error
}

// kekAttempts memoizes getOrCreateKEK per profile for the lifetime of the
// process: a profile's KEK doesn't change, so every call after the first for
// that profile can skip the OS keychain round trip (on macOS, keyring.Get
// forks a /usr/bin/security subprocess per call). Only the map lookup/insert
// itself is guarded by kekAttemptsMu — a short critical section; the
// expensive fetchKEK call runs inside that profile's own attempt.once.Do,
// outside the mutex, so all callers racing on one in-flight attempt observe
// the identical shared result.
//
// A failed attempt is NOT cached: if it fails, the map entry is removed so
// the next call gets a real retry instead of being stuck forever with a
// stale transient error (e.g. a momentarily locked keychain) — mirroring
// dek.go's per-(db,profile) retry-after-failure behavior.
var (
	kekAttemptsMu sync.Mutex
	kekAttempts   = map[string]*kekAttempt{}
)

// keyringIOMu serializes every raw keyring.Get/Set call this package makes.
// It exists for a narrower reason than kekAttemptsMu above (which only
// dedupes concurrent callers within one already-known-outcome attempt):
// peekKEK (dek.go's lockless fast path) and fetchOrCreateKEK (which dek.go's
// bootstrapDEKLocked calls while holding a cross-process DB lock that says
// nothing about the keychain) can otherwise run truly concurrently. Real OS
// keychain backends handle concurrent access safely on their own, but
// go-keyring's in-memory mock (used in tests) has no such protection. This
// mutex makes this package safe either way instead of relying on the
// backend's own guarantees.
var keyringIOMu sync.Mutex

// fetchKEK is the function getOrCreateKEK invokes to actually resolve a
// profile's KEK on first use. It is a package-level variable (rather than a
// direct call to fetchOrCreateKEK) purely so tests can substitute a stub
// that fails on demand to exercise the retry-after-failure path below.
var fetchKEK = fetchOrCreateKEK

// keyringGet/keyringSet are the raw OS keychain operations every KEK path
// goes through. Package-level variables (rather than direct keyring.Get /
// keyring.Set calls) so tests can substitute stubs that fail the way a host
// without an OS keyring does, exercising the file-based fallback behind
// peekKEK/fetchOrCreateKEK — the same stub-a-package-var pattern fetchKEK
// above uses.
var (
	keyringGet = keyring.Get
	keyringSet = keyring.Set
)

// getOrCreateKEK returns the 32-byte Key Encryption Key stored in the OS
// keychain for profileID (macOS Keychain / Linux Secret Service / Windows
// Credential Manager, via zalando/go-keyring — no cgo), generating and
// storing a new one on first use. The KEK never touches disk; only the DEK
// it wraps does (see dek.go) — except under the explicitly opted-in
// file-based fallback for hosts with no OS keyring (see filekeyring.go),
// where the KEK lives in a 0600 file under the vault directory instead.
func getOrCreateKEK(profileID string) ([]byte, error) {
	kekAttemptsMu.Lock()
	attempt, ok := kekAttempts[profileID]
	if !ok {
		attempt = &kekAttempt{}
		kekAttempts[profileID] = attempt
	}
	kekAttemptsMu.Unlock()

	attempt.once.Do(func() {
		attempt.kek, attempt.err = fetchKEK(profileID)
	})

	if attempt.err != nil {
		kekAttemptsMu.Lock()
		if kekAttempts[profileID] == attempt {
			delete(kekAttempts, profileID)
		}
		kekAttemptsMu.Unlock()
	}

	return attempt.kek, attempt.err
}

// peekKEK reads profileID's KEK from the OS keychain without creating one if
// it's missing — a pure read used by fetchOrCreateDEK's fast path (dek.go) to
// decide whether cross-process bootstrap locking is needed at all. Bypasses
// the in-process memoization layer, same as fetchOrCreateKEK. If the OS
// keyring itself is unavailable and MONOAGENT_ALLOW_FILE_KEYRING=1 is set,
// falls back to reading the file-based KEK (see filekeyring.go); without
// the env it fails closed.
func peekKEK(profileID string) (kek []byte, found bool, err error) {
	keyringIOMu.Lock()
	defer keyringIOMu.Unlock()

	stored, err := keyringGet(keyringService, kekAccount(profileID))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, false, nil
		}
		if fileKeyringEnabled() {
			return peekFileKEK(profileID)
		}
		return nil, false, fmt.Errorf("secrets: reading KEK from keychain: %w", err)
	}
	key, decodeErr := hex.DecodeString(stored)
	if decodeErr != nil {
		return nil, false, fmt.Errorf("secrets: decoding stored KEK: %w", decodeErr)
	}
	return key, true, nil
}

func fetchOrCreateKEK(profileID string) ([]byte, error) {
	keyringIOMu.Lock()
	defer keyringIOMu.Unlock()

	account := kekAccount(profileID)
	stored, err := keyringGet(keyringService, account)
	if err == nil {
		key, decodeErr := hex.DecodeString(stored)
		if decodeErr != nil {
			return nil, fmt.Errorf("secrets: decoding stored KEK: %w", decodeErr)
		}
		return key, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		// The OS keyring is unavailable (not merely empty). With the
		// explicit opt-in env, fall back to the file-based KEK; otherwise
		// fail closed exactly as before the fallback existed.
		if fileKeyringEnabled() {
			return fetchOrCreateFileKEK(profileID)
		}
		return nil, fmt.Errorf("secrets: reading KEK from keychain: %w", err)
	}

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		return nil, fmt.Errorf("secrets: generating KEK: %w", err)
	}
	if err := keyringSet(keyringService, account, hex.EncodeToString(kek)); err != nil {
		return nil, fmt.Errorf("secrets: storing KEK in keychain: %w", err)
	}
	return kek, nil
}

// fetchLegacyKEK reads the single pre-per-profile KEK every profile used to
// share, under its fixed "kek" account name. Read-only — never creates one.
// Used only by the vault-key migration to unwrap existing secrets so they
// can be re-encrypted under a fresh per-profile key; every other code path
// uses the per-profile functions above.
func fetchLegacyKEK() (kek []byte, found bool, err error) {
	keyringIOMu.Lock()
	defer keyringIOMu.Unlock()

	stored, err := keyringGet(keyringService, legacyKeyringAccount)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("secrets: reading legacy KEK from keychain: %w", err)
	}
	key, decodeErr := hex.DecodeString(stored)
	if decodeErr != nil {
		return nil, false, fmt.Errorf("secrets: decoding stored legacy KEK: %w", decodeErr)
	}
	return key, true, nil
}
