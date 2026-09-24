package tracesig

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	tok, err := Sign(key, "chn_abc_1", 5)
	if err != nil {
		t.Fatal(err)
	}
	chain, hop, ok := Verify(key, tok)
	if !ok || chain != "chn_abc_1" || hop != 5 {
		t.Fatalf("Verify(%q) = %q, %d, %v", tok, chain, hop, ok)
	}
}

// Changing any part of a token, or signing with another key, fails: in
// particular a caller cannot lower the hop of a token it replays.
func TestVerifyRejectsTampering(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	tok, _ := Sign(key, "chn_abc", 5)
	parts := strings.Split(tok, ".")
	other, _ := Sign([]byte(strings.Repeat("x", 32)), "chn_abc", 5)
	for name, bad := range map[string]string{
		"lower hop":     strings.Join([]string{parts[0], parts[1], "0", parts[3]}, "."),
		"other chain":   strings.Join([]string{parts[0], "chn_victim", parts[2], parts[3]}, "."),
		"other key":     other,
		"version":       strings.Join([]string{"v2", parts[1], parts[2], parts[3]}, "."),
		"leading zero":  strings.Join([]string{parts[0], parts[1], "05", parts[3]}, "."),
		"negative hop":  strings.Join([]string{parts[0], parts[1], "-5", parts[3]}, "."),
		"missing mac":   strings.Join(parts[:3], "."),
		"not a chain":   strings.Join([]string{parts[0], "../etc", parts[2], parts[3]}, "."),
		"empty":         "",
		"unsigned json": `{"chain_id":"chn_abc","hop":0}`,
	} {
		if _, _, ok := Verify(key, bad); ok {
			t.Errorf("%s: %q verified", name, bad)
		}
	}
}

func TestNewChainIDIsSignable(t *testing.T) {
	a, b := NewChainID(), NewChainID()
	if a == b || len(a) != len("chn_")+20 || !chainRe.MatchString(a) {
		t.Fatalf("NewChainID = %q, %q", a, b)
	}
}

func TestSignRejectsBadInput(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	if _, err := Sign(key, "not-a-chain", 1); err == nil {
		t.Error("signed a malformed chain id")
	}
	if _, err := Sign(key, "chn_a", -1); err == nil {
		t.Error("signed a negative hop")
	}
}

// The key is created once with mode 0600 and read back by every caller,
// including ones racing to create it.
func TestKeyIsCreatedOnceAndShared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "trace.key")
	var wg sync.WaitGroup
	keys := make([][]byte, 8)
	for i := range keys {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Bypass the cache so each goroutine really races on the file.
			k, err := create(path)
			if err != nil {
				t.Error(err)
			}
			keys[i] = k
		}(i)
	}
	wg.Wait()
	for i := 1; i < len(keys); i++ {
		if string(keys[i]) != string(keys[0]) {
			t.Fatal("racing creators ended up with different keys")
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v, want 0600", info.Mode().Perm())
	}
	k, err := keyAt(path)
	if err != nil || string(k) != string(keys[0]) {
		t.Fatalf("keyAt = %x, %v", k, err)
	}
}

func TestKeyRejectsAShortFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.key")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := keyAt(path); err == nil {
		t.Fatal("accepted a 5-byte key")
	}
}
