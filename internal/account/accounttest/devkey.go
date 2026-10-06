package accounttest

import (
	"crypto/ed25519"
	"encoding/hex"
)

// DevKID is the kid of the development signing key.
const DevKID = "monoagent-dev-1"

// devSeedHex is the seed of the fixed development key pair. It is a
// development-only key: builds with the devaccount tag will trust its public half
// (keys_devaccount.go returns no key until B1b wires it), and nothing else does, so
// publishing it gives no access to anything. A fake
// monoes.me (libraryfake) signs with it so a devaccount binary can sign in
// against the fake.
const devSeedHex = "0203903c037c7e989f9a31ac125086a822b629a82f58303218d5788978baf650"

// DevKeyPair returns the fixed development key pair: the public half is
// trusted by `-tags devaccount` builds, the private half signs for libraryfake.
func DevKeyPair() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed, err := hex.DecodeString(devSeedHex)
	if err != nil || len(seed) != ed25519.SeedSize {
		panic("accounttest: the development seed is malformed")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(ed25519.PublicKey), priv
}
