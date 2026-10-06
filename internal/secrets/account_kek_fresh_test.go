package secrets

import (
	"bytes"
	"encoding/hex"
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
