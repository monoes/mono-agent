package main

import (
	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/monomind"
)

// monomind.Exec asks a gate before it starts an agent turn, and the gate is
// account.Require: monomind cannot import internal/account without a test-only
// import cycle (see internal/monomind/account_gate.go), so the binary that has
// both installs it, once, before anything runs. A guard installed later (run())
// is what account.Require reads at each call.
func init() { monomind.SetAccountGate(account.Require) }
