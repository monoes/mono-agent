//go:build !devaccount

package account

// extraKeys is the build-tag slot for keys beyond the pinned set. A default
// build trusts the pinned set and nothing else. Whatever fills the slot must not
// take globalsMu: TrustedKeys calls it while holding the read lock.
func extraKeys() []Key { return nil }
