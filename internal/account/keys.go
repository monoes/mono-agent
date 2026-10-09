package account

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
)

// Key is a pinned verification key: the Ed25519 public key monoes.me signs
// access tokens with, and the kid its tokens name it by.
type Key struct {
	KID    string
	Public ed25519.PublicKey
}

// pinnedKeys is the set a release trusts: the current signing key and the next
// one, so a rotation never strands a client (spec §4.7).
//
//   - GB6kESA9…  the key monoes.me's JWT plugin generated and publishes today
//     (EdDSA, Ed25519), pinned 2026-10-09 from https://monoes.me/api/auth/jwks.
//     Kept so tokens issued before the server switches to the configured key
//     still verify; drop it in a later release once none are in use.
//   - iPa0jEFW…  the CURRENT key: the Worker secret MONOAGENT_JWT_PRIVATE_JWK holds its
//     private half (generated 2026-10-09; kid is its RFC 7638 thumbprint).
//   - nwholqtw…  the NEXT key: its private half is held offline and is NOT on the
//     server. The server must be switched to it only after a release that pins it
//     has been out long enough that clients have updated (spec §4.7, plan O5).
//
// Pinning keys does not turn enforcement on: that is the enforcement date, which
// stays zero (rollout.go). B5a must not set it while this set is empty
// (TestEnforcedBuildPinsAKey).
var pinnedKeys = []Key{
	pinKey("GB6kESA9qO98637VArEGR2EjW6wSyYqO", "ca570571b89cde8feb93c0e03581b78968ed75a953ac9bd349f01edf2ee9d26f"),
	pinKey("iPa0jEFWi05aKEmOZ-tQmDSVBJzuBIj-YT-UwVJJYr4", "5722b257d42cbfc2826f965c0e29a89d8ea371b6d39ff8433b42dc4a9644b6b2"),
	pinKey("nwholqtwhkaDi1bs1Byt1LwB5-7J9fMtzIuhv2i7qW0", "0f2a84c65cec9e26e148018cae0314168534273a148d2d12e12fc4f0deb720f3"),
}

// keysOverride replaces the whole trusted set while a test holds it
// (SetTrustedKeysForTest); keysOverridden tells an empty override from none.
var (
	keysOverride   []Key
	keysOverridden bool
)

// TrustedKeys returns the pinned set; under the devaccount build tag it also
// holds the development key. A copy: callers cannot change what is trusted.
func TrustedKeys() []Key {
	globalsMu.RLock()
	defer globalsMu.RUnlock()
	if keysOverridden {
		return cloneKeys(keysOverride)
	}
	return mergeKeys(pinnedKeys, extraKeys())
}

// mergeKeys is the pinned keys followed by the extra ones, every key a copy: the
// result is the caller's to change, and the development key of a devaccount
// build is not. It takes both sets as arguments so that a test can give it extra
// keys, which a default build has none of.
func mergeKeys(pinned, extra []Key) []Key {
	return append(cloneKeys(pinned), cloneKeys(extra)...)
}

// lookupKey finds a trusted key by kid.
func lookupKey(kid string) (Key, bool) {
	for _, k := range TrustedKeys() {
		if k.KID == kid {
			return k, true
		}
	}
	return Key{}, false
}

func cloneKeys(keys []Key) []Key {
	out := make([]Key, len(keys))
	for i, k := range keys {
		out[i] = Key{KID: k.KID, Public: append(ed25519.PublicKey(nil), k.Public...)}
	}
	return out
}

// pinKey builds a Key from a hex-encoded Ed25519 public key. It panics on a
// malformed key: a bad constant is a developer error that the tests catch.
func pinKey(kid, publicHex string) Key {
	pub, err := hex.DecodeString(publicHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		panic(fmt.Sprintf("account: pinned key %q is not a hex-encoded Ed25519 public key", kid))
	}
	return Key{KID: kid, Public: pub}
}
