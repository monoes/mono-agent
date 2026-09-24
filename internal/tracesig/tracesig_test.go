package tracesig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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
	join := func(p ...string) string { return strings.Join(p, ".") }
	for name, bad := range map[string]string{
		"lower hop":     join(parts[0], parts[1], "0", parts[3], parts[4]),
		"other chain":   join(parts[0], "chn_victim", parts[2], parts[3], parts[4]),
		"later expiry":  join(parts[0], parts[1], parts[2], "99999999999", parts[4]),
		"other key":     other,
		"version":       join("v1", parts[1], parts[2], parts[3], parts[4]),
		"v1 shape":      join("v1", parts[1], parts[2], parts[4]),
		"leading zero":  join(parts[0], parts[1], "05", parts[3], parts[4]),
		"negative hop":  join(parts[0], parts[1], "-5", parts[3], parts[4]),
		"missing mac":   join(parts[:4]...),
		"not a chain":   join(parts[0], "../etc", parts[2], parts[3], parts[4]),
		"empty":         "",
		"unsigned json": `{"chain_id":"chn_abc","hop":0}`,
	} {
		if _, _, ok := Verify(key, bad); ok {
			t.Errorf("%s: %q verified", name, bad)
		}
	}
}

// A token verifies for TTL after it was signed and not after, so a leaked
// or logged token cannot be replayed onto its chain for ever.
func TestTokensExpire(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	start := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	t.Cleanup(func() { now = time.Now })
	now = func() time.Time { return start }
	tok, err := Sign(key, "chn_abc", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		at   time.Duration
		want bool
	}{{0, true}, {TTL - time.Second, true}, {TTL, false}, {TTL + time.Hour, false}} {
		now = func() time.Time { return start.Add(c.at) }
		if _, _, ok := Verify(key, tok); ok != c.want {
			t.Errorf("%s after signing: verified %v, want %v", c.at, ok, c.want)
		}
	}
	// An expiry further out than TTL was not written by Sign, even under
	// the right key (a clock that jumped back a day, say).
	now = func() time.Time { return start.Add(-24 * time.Hour) }
	if _, _, ok := Verify(key, tok); ok {
		t.Error("a token expiring more than TTL ahead verified")
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

// A key others can read is refused, like an ssh private key.
func TestKeyRefusesAWorldReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not describe Windows ACLs")
	}
	path := filepath.Join(t.TempDir(), "trace.key")
	if err := os.WriteFile(path, []byte(strings.Repeat("k", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := keyAt(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("keyAt on a 0644 key = %v, want a refusal naming chmod 600", err)
	}
}

// Without hard links the exclusive-create fallback still yields one key.
func TestCreateExclusiveFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.key")
	a, err := createExclusive(path, []byte(strings.Repeat("a", 32)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := createExclusive(path, []byte(strings.Repeat("b", 32)))
	if err != nil || string(a) != string(b) {
		t.Fatalf("second creator got %q, %v; want the first key", b, err)
	}
}
