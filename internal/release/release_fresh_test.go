package release

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (r *rig) serve(t *testing.T, m Manifest) {
	t.Helper()
	m.Schema = 1
	var err error
	if r.manifest, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	r.sig = sign(r.priv, r.pub, r.manifest)
}

func TestExpiresAt(t *testing.T) {
	r := newRig(t)
	r.client.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) }

	r.serve(t, Manifest{Version: "v2.0.0", ReleasedAt: "2026-10-08T10:00:00Z", ExpiresAt: "2026-10-20T00:00:00Z"})
	if _, err := r.client.Latest(context.Background()); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("expired manifest must be untrusted, got %v", err)
	}
	r.serve(t, Manifest{Version: "v2.0.0", ReleasedAt: "2026-10-08T10:00:00Z", ExpiresAt: "2026-12-01T00:00:00Z"})
	if _, err := r.client.Latest(context.Background()); err != nil {
		t.Fatalf("unexpired manifest: %v", err)
	}
	r.serve(t, Manifest{Version: "v2.0.0", ReleasedAt: "2026-10-08T10:00:00Z", ExpiresAt: "tomorrow"})
	if _, err := r.client.Latest(context.Background()); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("malformed expires_at must be untrusted, got %v", err)
	}
}

func TestRollbackRefusedAcrossRuns(t *testing.T) {
	r := newRig(t)
	r.client.StatePath = filepath.Join(t.TempDir(), "state", "release-state.json")
	ctx := context.Background()

	r.serve(t, Manifest{Version: "v2.1.0", ReleasedAt: "2026-10-10T10:00:00Z"})
	if _, err := r.client.Latest(ctx); err != nil {
		t.Fatal(err)
	}
	// A validly signed but older manifest is replayed.
	r.serve(t, Manifest{Version: "v2.0.0", ReleasedAt: "2026-10-08T10:00:00Z"})
	if _, err := r.client.Latest(ctx); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("older manifest must be refused, got %v", err)
	}
	// Same version, earlier released_at.
	r.serve(t, Manifest{Version: "v2.1.0", ReleasedAt: "2026-10-09T10:00:00Z"})
	if _, err := r.client.Latest(ctx); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("earlier released_at must be refused, got %v", err)
	}
	// --force accepts it but does not lower the recorded highest.
	forced := *r.client
	forced.Force = true
	r.serve(t, Manifest{Version: "v2.0.0", ReleasedAt: "2026-10-08T10:00:00Z"})
	if _, err := forced.Latest(ctx); err != nil {
		t.Fatalf("force: %v", err)
	}
	if _, err := r.client.Latest(ctx); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("forcing must not lower the high-water mark, got %v", err)
	}
	// The same release and a newer one are fine.
	r.serve(t, Manifest{Version: "v2.1.0", ReleasedAt: "2026-10-10T10:00:00Z"})
	if _, err := r.client.Latest(ctx); err != nil {
		t.Fatalf("same release: %v", err)
	}
	r.serve(t, Manifest{Version: "v2.2.0", ReleasedAt: "2026-10-11T10:00:00Z"})
	if _, err := r.client.Latest(ctx); err != nil {
		t.Fatalf("newer release: %v", err)
	}
}

func TestParseKeyChecksID(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := ParseKey(KeyLine(pub)); err != nil {
		t.Fatal(err)
	}
	bad := "0000000000000000 " + base64.StdEncoding.EncodeToString(pub)
	if _, err := ParseKey(bad); err == nil {
		t.Fatal("a key id that is not KeyID(pub) must be rejected")
	}
}

func TestSigLineFormat(t *testing.T) {
	r := newRig(t)
	keys := []Key{{ID: KeyID(r.pub), Public: r.pub}}
	good := base64.StdEncoding.EncodeToString(ed25519.Sign(r.priv, r.manifest))
	for _, tc := range []struct{ desc, sig string }{
		{"no key id", good},
		{"short key id", "abcd " + good},
		{"extra field", KeyID(r.pub) + " " + good + " x"},
		{"bad base64", KeyID(r.pub) + " !!!"},
		{"short signature", KeyID(r.pub) + " " + base64.StdEncoding.EncodeToString([]byte("short"))},
		{"unpinned key id", "0123456789abcdef " + good},
	} {
		if _, err := VerifyWith(keys, r.manifest, []byte(tc.sig)); !errors.Is(err, ErrUntrusted) {
			t.Errorf("%s: want ErrUntrusted, got %v", tc.desc, err)
		}
	}
	// The #435 format: "<keyid> <b64sig>\n" over the exact manifest bytes,
	// signed with the 64-byte private key stored in the keyring.
	stored := base64.StdEncoding.EncodeToString(r.priv)
	raw, _ := base64.StdEncoding.DecodeString(stored)
	priv := ed25519.PrivateKey(raw)
	line := KeyID(priv.Public().(ed25519.PublicKey)) + " " + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, r.manifest)) + "\n"
	if _, err := VerifyWith(keys, r.manifest, []byte(line)); err != nil {
		t.Fatalf("round trip: %v", err)
	}
}
