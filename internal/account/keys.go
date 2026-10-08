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
// one, so a rotation never strands a client (spec §4.7). It is empty until
// spike S1 settles how monoes.me signs with a key that can be pinned; the first
// entry goes here as pinKey("<kid>", "<64 hex digits>"). B5a must not set the
// enforcement date while it is empty (TestEnforcedBuildPinsAKey).
var pinnedKeys = []Key{}

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
