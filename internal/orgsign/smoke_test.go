package orgsign

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// execSigner runs a real `monomind org sign <org> --yes` in root.
type execSigner struct{ bin string }

func (s execSigner) Sign(ctx context.Context, root, org string) error {
	cmd := exec.CommandContext(ctx, s.bin, "org", "sign", org, "--yes")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &exec.ExitError{ProcessState: cmd.ProcessState, Stderr: out}
	}
	return nil
}

// TestSmokeRealMonomind checks this package against a real monomind 2.21
// signature. Opt-in: set MONOAGENT_SMOKE_MONOMIND to a 2.21 binary
// installed in a scratch prefix, with HOME pointing at a scratch home.
func TestSmokeRealMonomind(t *testing.T) {
	bin := os.Getenv("MONOAGENT_SMOKE_MONOMIND")
	if bin == "" {
		t.Skip("MONOAGENT_SMOKE_MONOMIND not set")
	}
	t.Setenv("MONOMIND_ORGRT_OPERATOR_DIR", filepath.Join(t.TempDir(), "operator"))
	root := t.TempDir()
	write := func(body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, ".monomind", "orgs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".monomind", "orgs", "growth.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return SHA256([]byte(body))
	}
	const base = `{"name":"growth","goal":"grow","status":"stopped","schedule":null,"run_config":{"max_turns_per_message":2.0},` +
		`"roles":[{"id":"lead","title":"Lead","type":"lead","reports_to":null,"responsibilities":["lead"],"policy":{"git":"read"}}]}`
	sha := write(base)
	if st, _, _ := VerifyFile(root, "growth"); st.State != StateUnsigned {
		t.Fatalf("before signing: %+v", st)
	}
	if out := SignExact(context.Background(), execSigner{bin}, root, "growth", sha); !out.Signed {
		t.Fatalf("sign: %+v", out)
	}
	if st, _, _ := VerifyFile(root, "growth"); !st.OK() {
		t.Fatalf("after monomind signed: %+v", st)
	}
	write(`{"name":"growth","goal":"other goal","status":"running","schedule":null,"run_config":{"max_turns_per_message":2},` +
		`"roles":[{"id":"lead","title":"Boss","type":"lead","reports_to":null,"responsibilities":[],"policy":{"git":"read"}}]}`)
	if st, _, _ := VerifyFile(root, "growth"); !st.OK() {
		t.Fatalf("cosmetic edit: %+v", st)
	}
	write(`{"name":"growth","goal":"grow","status":"stopped","schedule":null,"run_config":{"max_turns_per_message":2},` +
		`"roles":[{"id":"lead","title":"Lead","type":"lead","reports_to":null,"responsibilities":["lead"],"policy":{"git":"push"}}]}`)
	if st, _, _ := VerifyFile(root, "growth"); st.State != StateChanged {
		t.Fatalf("policy edit: %+v", st)
	}
}
