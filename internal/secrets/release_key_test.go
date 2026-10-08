package secrets

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestReleaseSigningKeyRoundTrip(t *testing.T) {
	store := map[string]string{}
	oldGet, oldSet := keyringGet, keyringSet
	keyringGet = func(svc, acct string) (string, error) {
		if v, ok := store[svc+"/"+acct]; ok {
			return v, nil
		}
		return "", keyring.ErrNotFound
	}
	keyringSet = func(svc, acct, v string) error { store[svc+"/"+acct] = v; return nil }
	t.Cleanup(func() { keyringGet, keyringSet = oldGet, oldSet })

	if _, err := ReleaseSigningKey(); !errors.Is(err, ErrNoReleaseKey) {
		t.Fatalf("empty keychain: %v", err)
	}
	if err := StoreReleaseSigningKey("abc"); err != nil {
		t.Fatal(err)
	}
	if v, err := ReleaseSigningKey(); err != nil || v != "abc" {
		t.Fatalf("got %q %v", v, err)
	}
}
