package autostart

import (
	"testing"
	"time"
)

// A daemon stopped while its account guard renews the session waits for the refresh grant in flight (20 s)
// and then for the key-store write of its answer (10 s). A manager that kills it sooner loses the answer.
func TestTheStopGraceCoversTheGrantAndTheKeyStoreWrite(t *testing.T) {
	if stopGrace < 35*time.Second {
		t.Fatalf("stopGrace = %v: a service manager that waits less can kill a daemon that is saving the answer of a refresh", stopGrace)
	}
}
