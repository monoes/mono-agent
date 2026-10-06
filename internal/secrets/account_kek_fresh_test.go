package secrets

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zalando/go-keyring"
)

// countKeyringWrites counts the OS keychain writes from here on. The writes still
// reach the key store underneath.
func countKeyringWrites(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	orig := keyringSet
	t.Cleanup(func() { keyringSet = orig })
	keyringSet = func(service, user, password string) error {
		n.Add(1)
		return orig(service, user, password)
	}
	return &n
}

// The account's key store entry can be replaced while a process runs: a keychain
// reset, then a sign-in in another process that makes a new key. A daemon's next
// refresh then asks for the key with create=true, and it must get the key the key
// store holds now, without writing anything. A key remembered from an earlier call
// (the vault memoizes its keys for the life of the process) would seal the rotated
// refresh token under a key no process, this one included, can find again.
func TestAccountKEKCreateReturnsTheKeyTheKeyStoreHoldsNow(t *testing.T) {
	accountKEKTestHome(t, false)
	failOnPassphrasePrompt(t)
	first, found, err := AccountKEK(true, false) // the daemon's first refresh makes the key
	if err != nil || !found || len(first) != 32 {
		t.Fatalf("create: len=%d found=%v err=%v", len(first), found, err)
	}
	// Whichever path made it, the vault's memo holds that key from here on.
	if memo, err := getOrCreateKEK(accountKEKID); err != nil || !bytes.Equal(memo, first) {
		t.Fatalf("the vault's memo does not hold the first key (err %v)", err)
	}

	replaced := bytes.Repeat([]byte{0x5c}, 32)
	if err := keyring.Set(keyringService, kekAccount(accountKEKID), hex.EncodeToString(replaced)); err != nil {
		t.Fatal(err)
	}
	writes := countKeyringWrites(t)
	for _, c := range []struct{ create, interactive bool }{{true, false}, {true, true}, {false, false}, {false, true}} {
		got, found, err := AccountKEK(c.create, c.interactive)
		if err != nil || !found || !bytes.Equal(got, replaced) {
			t.Errorf("create=%v interactive=%v: found=%v err=%v remembered=%v, want the key the key store holds now", c.create, c.interactive, found, err, bytes.Equal(got, first))
		}
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("the calls wrote the key store %d times; with a key in place they must only read it", n)
	}
	if stored, err := keyring.Get(keyringService, kekAccount(accountKEKID)); err != nil || stored != hex.EncodeToString(replaced) {
		t.Errorf("the key store no longer holds the key that replaced the first one (err %v)", err)
	}
}

// Only a key store that answers "no entry" may get a new key. A read that fails (a
// locked keychain, a backend that does not answer) is returned as the error, and
// nothing is written: a new key would orphan the refresh token sealed under the
// old one, which the key store may hold again once it answers.
func TestAccountKEKCreateMakesNoKeyWhenTheKeyStoreFails(t *testing.T) {
	accountKEKTestHome(t, false)
	failOnPassphrasePrompt(t)
	locked := errors.New("keychain locked (forced in test)")
	origGet, origSet := keyringGet, keyringSet
	t.Cleanup(func() { keyringGet, keyringSet = origGet, origSet })
	keyringGet = func(service, user string) (string, error) { return "", locked }
	var writes atomic.Int32
	keyringSet = func(service, user, password string) error { writes.Add(1); return nil } // a write would succeed
	for _, interactive := range []bool{false, true} {
		if kek, found, err := AccountKEK(true, interactive); !errors.Is(err, locked) || found || kek != nil {
			t.Errorf("interactive=%v: found=%v err=%v, want the key store's error", interactive, found, err)
		}
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("a failing key store read made a key (%d writes)", n)
	}
}

// Creating calls of one process that find no key at once end with one key:
// keyringIOMu makes the read and the write of the create one step, so every call
// after the first reads the key the first one wrote. This is the in-process half
// of "one key": across processes the account's callers hold session.lock around
// every Seal, and the file keyring creates with O_EXCL (see accountKEKVault). The
// calls overlap only now and then, so the race is run many times.
func TestAccountKEKConcurrentCreatesInOneProcessEndWithOneKey(t *testing.T) {
	accountKEKTestHome(t, false)
	failOnPassphrasePrompt(t)
	writes := countKeyringWrites(t)
	const rounds, callers = 200, 8
	for round := range rounds {
		keyring.MockInit() // no key yet
		writes.Store(0)
		keys := make([][]byte, callers)
		errs := make([]error, callers)
		var start, done sync.WaitGroup
		start.Add(1)
		for i := range callers {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				keys[i], _, errs[i] = AccountKEK(true, i%2 == 0)
			}()
		}
		start.Done()
		done.Wait()
		stored, err := keyring.Get(keyringService, kekAccount(accountKEKID))
		if err != nil {
			t.Fatalf("round %d: no key was stored: %v", round, err)
		}
		for i := range callers {
			if errs[i] != nil || hex.EncodeToString(keys[i]) != stored {
				t.Fatalf("round %d, caller %d: err=%v, holds the stored key=%v", round, i, errs[i], hex.EncodeToString(keys[i]) == stored)
			}
		}
		if n := writes.Load(); n != 1 {
			t.Fatalf("round %d: the key store was written %d times, want once", round, n)
		}
	}
}

// A process that loses the race to create the account's file keyring adopts the
// winner's key: another process creates the file between this one's read and its
// create, O_EXCL refuses the second create, and the loser reads the file again.
func TestAccountKEKFileKeyringCreateRaceAdoptsTheWinnersKey(t *testing.T) {
	accountKEKTestHome(t, true) // the OS keychain has no entry, so the key goes to the file keyring
	winner := bytes.Repeat([]byte{0x77}, 32)
	const pass = "pw one"
	orig := filePassphraseFunc
	t.Cleanup(func() { filePassphraseFunc = orig })
	filePassphraseFunc = func() (string, error) {
		// The first ask comes after this process found no file and before it creates
		// one: the other process creates it now. A later ask changes nothing.
		if _, err := os.Stat(fileKeyringPath(accountKEKID)); errors.Is(err, os.ErrNotExist) {
			wrapped, err := wrapFileKEK(winner, pass)
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(fileKeyringPath(accountKEKID), wrapped, 0o600); err != nil {
				return "", err
			}
		}
		return pass, nil
	}
	if got, found, err := AccountKEK(true, true); err != nil || !found || !bytes.Equal(got, winner) {
		t.Fatalf("found=%v err=%v, want the key of the process that created the file first", found, err)
	}
	failOnPassphrasePrompt(t) // the loser remembered the passphrase that opened the winner's file
	if got, found, err := AccountKEK(false, true); err != nil || !found || !bytes.Equal(got, winner) {
		t.Errorf("the file keyring does not hold the winner's key: found=%v err=%v", found, err)
	}
}

// The sign-in's sealer under the file keyring asks for the passphrase once per
// process: the create that makes the file asks, and the seals and the opens after
// it use the passphrase that create remembered.
func TestAccountKEKInteractiveFileKeyringAsksForThePassphraseOnce(t *testing.T) {
	accountKEKTestHome(t, true)
	asks := 0
	orig := filePassphraseFunc
	t.Cleanup(func() { filePassphraseFunc = orig })
	filePassphraseFunc = func() (string, error) { asks++; return "pw one", nil }
	created, found, err := AccountKEK(true, true) // the sign-in's Seal makes the file keyring
	if err != nil || !found || len(created) != 32 {
		t.Fatalf("create: len=%d found=%v err=%v", len(created), found, err)
	}
	for _, create := range []bool{true, false} { // a second Seal, then an Open
		if got, found, err := AccountKEK(create, true); err != nil || !found || !bytes.Equal(got, created) {
			t.Errorf("create=%v: found=%v err=%v, want the created key", create, found, err)
		}
	}
	if asks != 1 {
		t.Errorf("asked for the passphrase %d times, want once", asks)
	}
}
