package main

import "testing"

func TestQueuedArgs(t *testing.T) {
	a, err := queuedArgs("growth")
	eqArgs(t, a, err, []string{"queued", "growth"})
	a, err = queuedArgs("  ")
	wantErr(t, a, err, "org name required")
}
