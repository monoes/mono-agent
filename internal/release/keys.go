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
// `monoagentcli release keygen` prints). It is EMPTY until the owner generates
// the key and pastes the line here; with no key the signed-manifest path is
// unavailable, never trusted.
var pinnedReleaseKeys = []string{}

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
	return Key{ID: f[0], Public: ed25519.PublicKey(raw)}, nil
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
