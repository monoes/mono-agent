package account

import (
	"bytes"
	"errors"
	"testing"
)

func TestMemorySealerRoundTrip(t *testing.T) {
	s := NewMemorySealer()
	plain := []byte("refresh-value-1")
	a, err := s.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Seal(plain)
	if bytes.Equal(a, b) {
		t.Fatal("two seals of one plaintext must differ (fresh nonce)")
	}
	if bytes.Contains(a, plain) {
		t.Fatal("the sealed blob contains the plaintext")
	}
	got, err := s.Open(a)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("Open did not return the sealed plaintext (match=%v, err=%v)", bytes.Equal(got, plain), err)
	}
	empty, _ := s.Seal(nil)
	if got, err := s.Open(empty); err != nil || len(got) != 0 {
		t.Fatalf("an empty plaintext must round-trip (len %d, err %v)", len(got), err)
	}
}

func TestSealedBlobsThatCannotBeOpened(t *testing.T) {
	s := NewMemorySealer()
	sealed, _ := s.Seal([]byte("refresh-value-1"))
	flipped := append([]byte(nil), sealed...)
	flipped[len(flipped)-1] ^= 1
	newer := append([]byte(nil), sealed...)
	newer[0] = 2
	cases := map[string][]byte{
		"another key":         func() []byte { b, _ := NewMemorySealer().Seal([]byte("x")); return b }(),
		"a flipped byte":      flipped,
		"truncated":           sealed[:len(sealed)-3],
		"shorter than a head": sealed[:5],
		"empty":               nil,
		"a newer version":     newer,
	}
	for name, blob := range cases {
		if _, err := s.Open(blob); !errors.Is(err, ErrKeyringUnavailable) {
			t.Errorf("%s: err = %v, want ErrKeyringUnavailable", name, err)
		}
	}
}

func TestKeyringSealerMapsEveryKeyStoreFailureToErrKeyringUnavailable(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	good := keyringSealer{kek: func(bool) ([]byte, bool, error) { return key, true, nil }}
	sealed, err := good.Seal([]byte("refresh-value-1"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := good.Open(sealed); err != nil || string(got) != "refresh-value-1" {
		t.Fatalf("the round trip failed (match=%v, err=%v)", string(got) == "refresh-value-1", err)
	}

	broken := errors.New("keychain locked")
	failing := keyringSealer{kek: func(bool) ([]byte, bool, error) { return nil, false, broken }}
	missing := keyringSealer{kek: func(bool) ([]byte, bool, error) { return nil, false, nil }}
	wrongKey := keyringSealer{kek: func(bool) ([]byte, bool, error) { return bytes.Repeat([]byte{9}, 32), true, nil }}
	shortKey := keyringSealer{kek: func(bool) ([]byte, bool, error) { return []byte("short"), true, nil }}
	for name, c := range map[string]struct {
		s    Sealer
		open bool
	}{
		"seal, key store failing": {failing, false},
		"open, key store failing": {failing, true},
		"open, key missing":       {missing, true},
		"open, wrong key":         {wrongKey, true},
		"seal, key of bad length": {shortKey, false},
	} {
		var err error
		if c.open {
			_, err = c.s.Open(sealed)
		} else {
			_, err = c.s.Seal([]byte("x"))
		}
		if !errors.Is(err, ErrKeyringUnavailable) {
			t.Errorf("%s: err = %v, want ErrKeyringUnavailable", name, err)
		}
	}
	if _, err := failing.Open(sealed); !errors.Is(err, broken) {
		t.Errorf("the cause must stay in the chain: %v", err)
	}
}

// The production sealers, over the in-memory keyring TestMain installs.
func TestKeyringSealersOverTheMockKeyring(t *testing.T) {
	sealed, err := NewKeyringSealer().Seal([]byte("refresh-value-1"))
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Sealer{"quiet": NewKeyringSealer(), "interactive": NewInteractiveKeyringSealer()} {
		if got, err := s.Open(sealed); err != nil || string(got) != "refresh-value-1" {
			t.Errorf("%s: Open failed (match=%v, err=%v)", name, string(got) == "refresh-value-1", err)
		}
	}
}
