package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/orgsign/orgsigntest"
)

// fakeSigningMonomind is a monomind of the given version that logs every
// call and answers `org sign` like 2.21 does: without --yes a review (plus
// the refusal footer) and exit 1; with --yes "signed". It signs nothing
// itself — recordingSigner writes the signature, as monomind would.
func fakeSigningMonomind(t *testing.T, version string) (logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	bin := filepath.Join(dir, "monomind")
	script := `#!/bin/sh
echo "$(pwd) $*" >> '` + logPath + `'
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"` + version + `","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1","org-tool-providers","org-endpoint-roles","org-federation","org-decision-attribution"]}'
  exit 0
fi
if [ "$1" = "org" ] && [ "$2" = "sign" ]; then
  if [ "$4" = "--check" ]; then
    echo '{"orgs":[{"org":"'"$3"'","state":"'"${FAKE_CHECK_STATE:-changed}"'","message":"from monomind"}]}'
    exit 1
  fi
  if [ "$4" = "--yes" ]; then echo "org $3: signed"; exit 0; fi
  printf '\033[1m\norg %s (changed):\033[0m\n  lead: runtime claude \302\267 git push \302\267 access scoped\n  Changed since the last signature:\n    roles.lead.policy.git: read -> push\n  1 protected path(s) would be quarantined as possible plants: .claude/settings.json \342\200\224 if they are yours, approve them with monomind org approve-paths <path>\nNot signed. Review the above, then sign it yourself in a terminal: monomind org sign <org> (or pass --yes).\n[ERROR] confirmation required (--yes)\n' "$3"
  exit 1
fi
if [ "$1" = "org" ] && [ "$2" = "run" ]; then
  echo "[ERROR] org $3: the definition has no operator signature — run monomind org sign $3 as the operator after reviewing the change"
  exit 1
fi
if [ "$1" = "org" ] && [ "$2" = "validate" ]; then echo "valid"; exit 0; fi
echo '{"v":1}'
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)
	return logPath
}

// recordingSigner runs the real `monomind org sign --yes` invocation
// against the fake, then writes the signature the way monomind would.
type recordingSigner struct {
	t     *testing.T
	calls []string
}

func (s *recordingSigner) Sign(ctx context.Context, root, org string) error {
	if err := (monomindOrgSigner{}).Sign(ctx, root, org); err != nil {
		return err
	}
	s.calls = append(s.calls, org)
	orgsigntest.Sign(s.t, root, org)
	return nil
}

// Check uses monomind's own --check, as the production signer does.
func (s *recordingSigner) Check(ctx context.Context, root, org string) (orgsign.Status, bool) {
	return monomindOrgSigner{}.Check(ctx, root, org)
}

// newSigningFixture is newOrgCLIFixture on a fake monomind 2.21 with a
// scratch operator dir and a recording signer.
func newSigningFixture(t *testing.T, version string) (*orgCLIFixture, *recordingSigner, string) {
	t.Helper()
	f := newOrgCLIFixture(t)
	logPath := fakeSigningMonomind(t, version)
	t.Setenv("MONOMIND_ORGRT_OPERATOR_DIR", filepath.Join(t.TempDir(), "operator"))
	for _, k := range []string{"MONOMIND_ORG_ROLE", "MONOMIND_SDK_AGENT", "MONOMIND_AGENT_EXEC", "MONOMIND_CLINE_TURN", "MONOMIND_AIDER"} {
		t.Setenv(k, "")
	}
	s := &recordingSigner{t: t}
	prev := orgSigner
	orgSigner = s
	t.Cleanup(func() { orgSigner = prev })
	return f, s, logPath
}

func (f *orgCLIFixture) signature(t *testing.T) orgsign.Status {
	t.Helper()
	st, _, err := orgsign.VerifyFile(f.root, "growth")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// editOutside changes the org file the way a worker or a hand edit would.
func (f *orgCLIFixture) editOutside(t *testing.T, mutate func(m map[string]interface{})) {
	t.Helper()
	path := filepath.Join(orgdesign.OrgsDir(f.root), "growth.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	mutate(m)
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func widenLeadPolicy(m map[string]interface{}) {
	for _, r := range m["roles"].([]interface{}) {
		role := r.(map[string]interface{})
		if role["id"] == "lead" {
			role["policy"] = map[string]interface{}{"git": "push"}
		}
	}
}

func TestOrgWriteResignsAVerifiedOrg(t *testing.T) {
	f, s, logPath := newSigningFixture(t, "2.21.0")
	orgsigntest.Sign(t, f.root, "growth")

	f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post")
	f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "publish_post")
	if st := f.signature(t); !st.OK() {
		t.Fatalf("after mono-agent's own edits: %+v", st)
	}
	if len(s.calls) < 2 {
		t.Fatalf("sign calls %v, want one per changing write", s.calls)
	}
	calls, _ := os.ReadFile(logPath)
	root, _ := filepath.EvalSymlinks(f.root)
	if !strings.Contains(string(calls), root+" org sign growth --yes") {
		t.Fatalf("monomind org sign not run in the project root:\n%s", calls)
	}
}

func TestOrgWriteAfterOutsideEditIsNotSigned(t *testing.T) {
	f, s, _ := newSigningFixture(t, "2.21.0")
	orgsigntest.Sign(t, f.root, "growth")
	f.editOutside(t, widenLeadPolicy)

	stderr := captureStderr(t, func() {
		f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post")
	})
	if len(s.calls) != 0 {
		t.Fatalf("signed an outside edit: %v", s.calls)
	}
	if st := f.signature(t); st.State != orgsign.StateChanged {
		t.Fatalf("state %+v, want changed", st)
	}
	if !strings.Contains(stderr, "not signed for this change") || !strings.Contains(stderr, "monoagentcli org sign growth") {
		t.Fatalf("no review notice on stderr: %q", stderr)
	}
}

// The daemon's reconcile (here `org reconcile`, the same pass) rewrites a
// file an outside edit left stale, and must not sign that edit with it.
func TestReconcileAfterOutsideEditNeverSigns(t *testing.T) {
	f, s, _ := newSigningFixture(t, "2.21.0")
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post")
	f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "publish_post")
	orgsigntest.Sign(t, f.root, "growth")
	s.calls = nil

	staleProvider(t, f.root) // what reconcile repairs
	f.editOutside(t, widenLeadPolicy)
	out := f.mustRun(t, "reconcile")
	orgs := out["orgs"].([]interface{})
	growth := orgs[0].(map[string]interface{})
	if growth["saved"] != true {
		t.Fatalf("reconcile did not rewrite the stale file: %v", growth)
	}
	if sig, _ := growth["signature"].(map[string]interface{}); sig["signed"] != false || !strings.Contains(sig["notice"].(string), "org sign growth") {
		t.Fatalf("reconcile outcome signature = %v", growth["signature"])
	}
	if len(s.calls) != 0 {
		t.Fatalf("reconcile signed an outside edit: %v", s.calls)
	}
	if providerCommand(t, f.root) != selfExecutable() {
		t.Fatal("provider not repaired")
	}
	if st := f.signature(t); st.OK() {
		t.Fatalf("outside edit ended up signed: %+v", st)
	}
}

func TestOrgSignCommand(t *testing.T) {
	f, s, logPath := newSigningFixture(t, "2.21.0")
	f.editOutside(t, widenLeadPolicy)

	st := f.mustRun(t, "sign", "growth", "--status")
	if st["supported"] != true || st["state"] != "unsigned" || st["signed"] != nil {
		t.Fatalf("status = %v", st)
	}

	// Without --yes (and not on a terminal): the review, nothing signed.
	rev := f.mustRun(t, "sign", "growth")
	review, _ := rev["review"].(string)
	// Everything monomind reports stays in the review, including paths
	// waiting for `org approve-paths` (signing approves none of them).
	if !strings.Contains(review, "git push") || !strings.Contains(review, "org approve-paths") ||
		strings.Contains(review, "confirmation required") || strings.Contains(review, "\x1b[") {
		t.Fatalf("review = %q", review)
	}
	if rev["signed"] != nil || len(s.calls) != 0 || rev["sha256"] == "" {
		t.Fatalf("review signed or lacks sha256: %v (calls %v)", rev, s.calls)
	}
	calls, _ := os.ReadFile(logPath)
	if !strings.Contains(string(calls), " org sign growth\n") {
		t.Fatalf("review did not run `monomind org sign growth` without --yes:\n%s", calls)
	}

	// A file that changed since the review is not signed.
	if _, err := f.run(t, "sign", "growth", "--yes", "--expect-sha256", strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "changed after") {
		t.Fatalf("stale review signed: %v", err)
	}
	if len(s.calls) != 0 {
		t.Fatalf("signed a stale review: %v", s.calls)
	}

	signed := f.mustRun(t, "sign", "growth", "--yes", "--expect-sha256", rev["sha256"].(string))
	if signed["signed"] != true || len(s.calls) != 1 {
		t.Fatalf("sign = %v (calls %v)", signed, s.calls)
	}
	if st := f.signature(t); !st.OK() {
		t.Fatalf("after sign: %+v", st)
	}

	// Inside an org role or agent turn, never.
	f.editOutside(t, func(m map[string]interface{}) { m["runtime"] = "codex" })
	t.Setenv("MONOMIND_AGENT_EXEC", "1")
	if _, err := f.run(t, "sign", "growth", "--yes"); err == nil || !strings.Contains(err.Error(), "only the operator") {
		t.Fatalf("signed in an agent turn: %v", err)
	}
	if len(s.calls) != 1 {
		t.Fatalf("calls %v", s.calls)
	}
}

func TestConfirmOrgSign(t *testing.T) {
	var shown strings.Builder
	if !confirmOrgSign(&shown, strings.NewReader("y\n"), "growth", "org growth (unsigned):") {
		t.Fatal("yes declined")
	}
	if !strings.Contains(shown.String(), "org growth (unsigned):") || !strings.Contains(shown.String(), `Sign org "growth"`) {
		t.Fatalf("prompt = %q", shown.String())
	}
	if confirmOrgSign(&shown, strings.NewReader("\n"), "growth", "") {
		t.Fatal("empty answer signed")
	}
}

// Below 2.21 nothing about signing runs: writes don't sign, starts aren't
// checked, and `org sign` says it isn't needed.
func TestOrgSigningGatedOnMonomind221(t *testing.T) {
	f, s, logPath := newSigningFixture(t, "2.20.3")
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post")
	if len(s.calls) != 0 {
		t.Fatalf("signed on 2.20: %v", s.calls)
	}
	st := f.mustRun(t, "sign", "growth", "--status")
	if st["supported"] != false {
		t.Fatalf("status on 2.20 = %v", st)
	}
	if _, err := f.run(t, "sign", "growth", "--yes"); err == nil || !strings.Contains(err.Error(), "2.21") {
		t.Fatalf("sign on 2.20: %v", err)
	}
	calls, _ := os.ReadFile(logPath)
	if strings.Contains(string(calls), "org sign") {
		t.Fatalf("ran org sign on 2.20:\n%s", calls)
	}
}

// monomind 2.21 refuses to start an unsigned org; the CLI says so up
// front, with the sign command and a machine-readable code.
func TestOrgRunRefusedForSignature(t *testing.T) {
	f, _, logPath := newSigningFixture(t, "2.21.0")
	_, err := f.run(t, "run", "growth")
	se, ok := monomind.AsOrgSignatureError(err)
	if !ok || se.Status.State != orgsign.StateUnsigned || !strings.Contains(err.Error(), "monoagentcli org sign growth") {
		t.Fatalf("run err = %v", err)
	}
	if se.JSONErrorFields()["code"] != "org_not_signed" {
		t.Fatalf("fields = %v", se.JSONErrorFields())
	}
	calls, _ := os.ReadFile(logPath)
	if strings.Contains(string(calls), "org run") {
		t.Fatalf("started monomind for an org it would refuse:\n%s", calls)
	}

	// A signed org starts (monomind's own refusal text is classified too).
	orgsigntest.Sign(t, f.root, "growth")
	_, err = f.run(t, "run", "growth")
	if _, ok := monomind.AsOrgSignatureError(err); !ok {
		t.Fatalf("monomind's refusal not classified: %v", err)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// On monomind 2.22 the state comes from monomind's own read-only check
// (`org sign --check --format json`), run in the project root; on 2.21
// that command does not exist and is never run.
func TestOrgSignUsesMonomindCheck(t *testing.T) {
	f, _, logPath := newSigningFixture(t, "2.22.0")
	t.Setenv("FAKE_CHECK_STATE", "signed") // no sidecar: the Go check alone says unsigned
	st := f.mustRun(t, "sign", "growth", "--status")
	if st["state"] != "signed" {
		t.Fatalf("status = %v, want monomind's verdict", st)
	}
	calls, _ := os.ReadFile(logPath)
	root, _ := filepath.EvalSymlinks(f.root)
	if !strings.Contains(string(calls), root+" org sign growth --check --format json") {
		t.Fatalf("--check not run in the project root:\n%s", calls)
	}

	f21, _, log21 := newSigningFixture(t, "2.21.0")
	if st := f21.mustRun(t, "sign", "growth", "--status"); st["state"] != "unsigned" {
		t.Fatalf("2.21 status = %v", st)
	}
	if calls, _ := os.ReadFile(log21); strings.Contains(string(calls), "--check") {
		t.Fatalf("ran --check on 2.21:\n%s", calls)
	}
}

// No mono-agent path signs from inside an org role's or agent turn's
// process tree (monomind's own markers): the write happens, unsigned,
// with a notice naming the marker, and monomind is never asked to sign.
func TestNoSigningFromRoleContext(t *testing.T) {
	for _, marker := range []string{"MONOMIND_ORG_ROLE", "MONOMIND_SDK_AGENT", "MONOMIND_AGENT_EXEC", "MONOMIND_CLINE_TURN", "MONOMIND_AIDER"} {
		t.Run(marker, func(t *testing.T) {
			f, s, logPath := newSigningFixture(t, "2.21.0")
			orgsigntest.Sign(t, f.root, "growth")
			t.Setenv(marker, "1")
			stderr := captureStderr(t, func() {
				f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post")
			})
			if len(s.calls) != 0 || !strings.Contains(stderr, marker) {
				t.Fatalf("calls %v, stderr %q", s.calls, stderr)
			}
			if _, err := f.run(t, "sign", "growth", "--yes"); err == nil {
				t.Fatal("org sign --yes ran in a role context")
			}
			if calls, _ := os.ReadFile(logPath); strings.Contains(string(calls), "--yes") {
				t.Fatalf("monomind asked to sign:\n%s", calls)
			}
		})
	}
}
