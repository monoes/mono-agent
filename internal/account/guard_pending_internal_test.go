package account

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// These tests are in package account for what is not exported: the constant that
// bounds a retry (A24), and the helpers of the refresh that is in flight.

// monoes.me answers a refresh token that it rotated away with the same answer for
// 300 seconds, and takes it for theft after that. A retry is only safe inside that
// window, and it must leave room for the call's own duration (up to
// refreshCallTimeout, 20 s), the wait for the key store (keyStoreTimeout, 10 s) and
// two clocks that do not run at the same rate: 240 s is the value of the contract.
func TestThePendingRetryWindowIsFourMinutesAndLeavesRoomInTheServersFive(t *testing.T) {
	const serversReuseWindow = 300 * time.Second
	if pendingRetryWindow != 240*time.Second {
		t.Fatalf("pendingRetryWindow = %v, want 240 s (index §3.2)", pendingRetryWindow)
	}
	if room := serversReuseWindow - pendingRetryWindow; room < refreshCallTimeout+keyStoreTimeout {
		t.Fatalf("a retry made at the end of the window reaches the server %v before its reuse window ends, want at least a call (%v) and a key store wait (%v)",
			room, refreshCallTimeout, keyStoreTimeout)
	}
}

// Whether the outcome of a grant is known is a fact about the failure, and the
// fail-safe reading is the zero value: a Refresher that never heard of the field
// reports an outcome that is unknown, which the guard treats as a refresh token
// that may have been rotated.
func TestATransientErrorIsUnsettledUnlessItSaysOtherwise(t *testing.T) {
	var zero TransientError
	if zero.Settled {
		t.Fatal("the zero value of TransientError is settled: a Refresher that does not set the field would be read as one whose outcome is known")
	}
	if (&TransientError{Reason: ReasonUnreachable, Err: errors.New("connection reset")}).Settled {
		t.Fatal("a TransientError built without Settled is settled")
	}
	// Settled says what happened to the token, not what to tell a person: the text of the error is the same.
	plain := &TransientError{Reason: ReasonUnreachable, Err: errors.New("no route")}
	settled := &TransientError{Reason: ReasonUnreachable, Err: errors.New("no route"), Settled: true}
	if plain.Error() != settled.Error() || !strings.Contains(plain.Error(), "no route") {
		t.Fatalf("Settled changes the text of the error: %q and %q", plain.Error(), settled.Error())
	}
	if !errors.Is(settled, settled.Err) || errors.Unwrap(settled) != settled.Err {
		t.Fatal("a settled TransientError no longer wraps its cause")
	}
}
