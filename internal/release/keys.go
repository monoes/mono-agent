package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// pinnedReleaseKeys are the release signing public keys this build trusts,
// one "<key-id> <base64 ed25519 public key>" line each (the line
// `monoagentcli release keygen` prints). With no key the signed-manifest path
// is unavailable, never trusted. The private half is the RELEASE_SIGNING_KEY
// secret of the release environment (docs/release-runbook.md).
var pinnedReleaseKeys = []string{
	"3b3e972f459a3151 /GDrvFCQb64EBe+tYDkFWNFXeiwysdr4sAB8kc+b2kU=",
}

// revokedReleaseKeyIDs lists key ids whose signatures are refused even while the key is still in
// pinnedReleaseKeys, so a release can ship with the old and the new key both pinned and the old
// one revoked: a client that has this list rejects anything the old key signs.
var revokedReleaseKeyIDs = map[string]bool{}

// Key is a pinned public key.
type Key struct {
	ID     string
	Public ed25519.PublicKey
}

// KeyID is the id of a public key: the first 8 bytes of its SHA-256, hex.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// KeyLine is the "<id> <base64 pub>" line for pinnedReleaseKeys.
func KeyLine(pub ed25519.PublicKey) string {
	return KeyID(pub) + " " + base64.StdEncoding.EncodeToString(pub)
}

// ParseKey parses one pinnedReleaseKeys line.
func ParseKey(line string) (Key, error) {
	f := strings.Fields(line)
	if len(f) != 2 {
		return Key{}, fmt.Errorf("release key: want \"<id> <base64 public key>\"")
	}
	raw, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return Key{}, fmt.Errorf("release key %s: not a base64 ed25519 public key", f[0])
	}
	pub := ed25519.PublicKey(raw)
	if f[0] != KeyID(pub) {
		return Key{}, fmt.Errorf("release key %s: the id does not match the key (want %s)", f[0], KeyID(pub))
	}
	return Key{ID: f[0], Public: pub}, nil
}

// PinnedKeys returns the keys compiled into this build. A malformed line is
// skipped: it must not make the other keys, or the binary, unusable.
func PinnedKeys() []Key {
	var out []Key
	for _, l := range pinnedReleaseKeys {
		if k, err := ParseKey(l); err == nil {
			out = append(out, k)
		}
	}
	return out
}
