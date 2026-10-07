//go:build devaccount

package account

// The development signing key: the public half of accounttest.DevKeyPair.
// Builds with the devaccount tag trust it and nothing else does (spec D24), so
// a developer or a CI job can sign in against libraryfake, which signs with the
// private half.
const (
	devKID          = "monoagent-dev-1"
	devPublicKeyHex = "b2f5cb7a39788e27efd2b9c4f96873b495b61933f28c9c42904be76cd4328cff"
)

func extraKeys() []Key { return []Key{pinKey(devKID, devPublicKeyHex)} }
