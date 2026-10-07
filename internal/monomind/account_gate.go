package monomind

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// ErrNoAccountGate is what Exec returns in a process that never installed an
// account gate. It refuses even before the enforcement date, so a binary that
// starts running turns without the gate fails at once instead of running
// unguarded.
var ErrNoAccountGate = errors.New("monomind: no monoes.me account gate is installed in this process")

// accountGate is what Exec asks before it starts a turn (spec section 6.2). It is
// a hook and not an import of internal/account on purpose: internal/storage
// imports this package, internal/secrets' tests import internal/storage, and
// internal/account imports internal/secrets, so an import here closes a cycle
// that `go vet ./internal/secrets/` rejects. cmd/monoagentcli installs
// account.Require when it starts (cmd/monoagentcli/monomind_gate.go).
var accountGate atomic.Pointer[func(context.Context) error]

// SetAccountGate installs the gate Exec asks; nil removes it.
func SetAccountGate(gate func(ctx context.Context) error) {
	if gate == nil {
		accountGate.Store(nil)
		return
	}
	accountGate.Store(&gate)
}

// inTestBinary is testing.Testing; a test replaces it to see the default-deny.
var inTestBinary = testing.Testing

// requireAccount is Exec's first statement. With no gate installed it refuses,
// except in a test binary, where the suites that never heard of the account run
// as they always did.
func requireAccount(ctx context.Context) error {
	if gate := accountGate.Load(); gate != nil {
		return (*gate)(ctx)
	}
	if inTestBinary() {
		return nil
	}
	return ErrNoAccountGate
}
