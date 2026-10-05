//go:build devaccount

package account

// extraKeys is where a devaccount build adds its development signing key.
// B1b puts it here (the public half of accounttest.DevKeyPair); until then a
// devaccount build trusts the pinned set only.
func extraKeys() []Key { return nil }
