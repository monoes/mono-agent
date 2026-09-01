package monomind

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// TestSmoke_RealMonomindHandshake exercises Handshake against a genuinely
// installed `monomind` binary — every other test in this package uses a
// fake stub under testdata/. This is what closes the CI gap identified in
// docs/plans/local-agent-monomind-delegation.md §11 (issue #23): a
// protocol/version mismatch between this repo's client and the pinned
// Monomind release should fail CI, not surface as a runtime error for
// users.
//
// Skipped unless MONOMIND_SMOKE=1 is set, since it requires a real
// `monomind` on PATH — CI sets this after installing the pinned version
// from .mcp.json; local `go test ./...` runs don't need monomind installed
// and shouldn't fail without it.
func TestSmoke_RealMonomindHandshake(t *testing.T) {
	if os.Getenv("MONOMIND_SMOKE") != "1" {
		t.Skip("set MONOMIND_SMOKE=1 to run against a real installed monomind binary")
	}

	bin, err := exec.LookPath("monomind")
	if err != nil {
		t.Fatalf("MONOMIND_SMOKE=1 but monomind is not on PATH: %v", err)
	}

	vi, err := Handshake(context.Background(), bin)
	if err != nil {
		t.Fatalf("Handshake against real monomind (%s) failed: %v", bin, err)
	}
	if vi.Version == "" {
		t.Fatal("Handshake returned an empty version")
	}
	t.Logf("monomind %s (protocol v%d) at %s", vi.Version, vi.V, bin)
}
