// Package doortest is the table every door's tests walk: one row for each state
// a door is judged in. It is test support, imported only from _test.go files.
package doortest

import "github.com/monoes/mono-agent/internal/account/accounttest"

// Row is one state of the account and what a door does in it.
type Row struct {
	Name string
	Mode accounttest.Mode
	// Refused is whether a request is turned away in this state.
	Refused bool
	// State and Reason are what a door that reports the account says of it. They
	// are empty for Dormant, whose verdict is the fixture's business: nothing is
	// refused whatever it says.
	State, Reason string
	// Enforced is what such a door says of enforcement: false only while dormant.
	Enforced bool
}

// Modes covers the five fixtures of accounttest. Grace and Dormant must be
// allowed: a login that is offline for under 24 hours keeps working, and until
// the enforcement date nothing locks.
var Modes = []Row{
	{"signed in", accounttest.SignedIn, false, "ok", "", true},
	{"grace", accounttest.InGrace, false, "grace", "unreachable", true},
	{"dormant", accounttest.Dormant, false, "", "", false},
	{"locked, no login", accounttest.LockedNoLogin, true, "locked", "not_logged_in", true},
	{"locked, refused", accounttest.LockedRefused, true, "locked", "refused", true},
}
