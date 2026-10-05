package monomind

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Texts captured from monomind v2.24.1 (see the PR).
const (
	roleSignRefusal = "Refusing: MONOMIND_ORG_ROLE is set — this is an org role or agent-exec process. Only the operator signs org definitions; run this yourself in a terminal."
	r6Refusal       = `Could not start org sec: org "sec" cannot start on this host: the authority mask (bubblewrap) is unavailable on this host (bwrap not found (ENOENT)), so mail digests, the envelope key and native runner copies are protected by file-tool rules alone; the SDK sandbox is unavailable on this host (bwrap and socat not found on PATH (install bubblewrap and socat)), so shell commands run without the read and write denials`
	r1Refusal       = `Could not start org sec: org root /mnt/nas/proj is on a network filesystem (nfs4); a sections org needs a local filesystem so one daemon can own it`
	heldRefusal     = `Could not start org sec: org "sec" is already owned by another daemon on /home/u/proj`
)

// fakeOrgMonomind is a monomind whose `org` subcommands run body (sh),
// with the handshake answered. Returns the project root.
func fakeOrgMonomind(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	bin := filepath.Join(t.TempDir(), "monomind") // outside the project
	script := "#!/bin/sh\n" +
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"2.24.1","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'; exit 0; fi` + "\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOverride, bin)
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)
	return t.TempDir()
}

func sh(s string) string { return "cat <<'EOT'\n" + s + "\nEOT\n" }

// A sign attempt from an org role fails with monomind's refusal verbatim:
// the review never passes it off as a review, and the sign carries it.
func TestRoleSignAttemptCarriesMonomindsRefusal(t *testing.T) {
	root := fakeOrgMonomind(t, sh(roleSignRefusal)+"echo '[ERROR] refused: role context (MONOMIND_ORG_ROLE)'\nexit 1")
	if rv, err := OrgSignReview(context.Background(), root, "acme"); err == nil || !strings.Contains(err.Error(), roleSignRefusal) {
		t.Fatalf("review = %+v, %v: want monomind's refusal as the error", rv, err)
	}
	if _, err := OrgSign(context.Background(), root, "acme", ""); err == nil || !strings.Contains(err.Error(), roleSignRefusal) {
		t.Fatalf("sign: %v", err)
	}
}

func TestStartRefusalExtracted(t *testing.T) {
	for name, tc := range map[string]struct{ text, kind string }{
		"R6":   {"noise\n  Total estimate: ~$1.80\n" + r6Refusal + "\n[ERROR] org start failed", RefusalPreflight},
		"R1":   {r1Refusal, RefusalDaemonLock},
		"held": {heldRefusal, RefusalDaemonLock},
	} {
		got := asStartRefusal("sec", tc.text)
		if got == nil || got.Kind != tc.kind {
			t.Fatalf("%s: %+v", name, got)
		}
		if !strings.Contains(tc.text, got.Text) || strings.Contains(got.Text, "\n") || !strings.Contains(got.Error(), got.Text) || got.Hint == "" {
			t.Errorf("%s: text %q hint %q", name, got.Text, got.Hint)
		}
	}
	if asStartRefusal("sec", "org run failed: boom") != nil {
		t.Error("an unrelated failure is not a start refusal")
	}
}

// org run prints the refusal on stdout and only "[ERROR] org start failed"
// on stderr: the message must reach the caller, not that bare line.
func TestOrgRunSurfacesStartRefusal(t *testing.T) {
	root := fakeOrgMonomind(t, sh(r6Refusal)+"echo '[ERROR] org start failed' >&2\nexit 1")
	_, err := OrgRun(context.Background(), root, "sec", "", false)
	ref, ok := AsStartRefusal(err)
	if !ok || ref.Kind != RefusalPreflight || !strings.Contains(err.Error(), r6Refusal) {
		t.Fatalf("err = %v", err)
	}
}

func TestOrgResumeSurfacesStartRefusal(t *testing.T) {
	text := `org "sec" cannot resume on this host: the SDK sandbox is unavailable on this host (bwrap and socat not found on PATH)`
	root := fakeOrgMonomind(t, sh(text)+"exit 1")
	_, err := OrgResume(context.Background(), root, "sec")
	if ref, ok := AsStartRefusal(err); !ok || ref.Kind != RefusalPreflight || !strings.Contains(err.Error(), text) {
		t.Fatalf("err = %v", err)
	}
}

// A detached start that dies at once (a refusal) is reported; one that
// stays up is not held up.
func TestOrgRunStartReportsEarlyRefusal(t *testing.T) {
	old := startWatch
	startWatch = 2 * time.Second
	t.Cleanup(func() { startWatch = old })

	root := fakeOrgMonomind(t, sh(r1Refusal)+"echo '[ERROR] org start failed' >&2\nexit 1")
	err := OrgRunStart(context.Background(), root, "sec", "")
	if ref, ok := AsStartRefusal(err); !ok || ref.Kind != RefusalDaemonLock || !strings.Contains(err.Error(), r1Refusal) {
		t.Fatalf("err = %v", err)
	}

	startWatch = 300 * time.Millisecond
	root = fakeOrgMonomind(t, "sleep 5")
	if err := OrgRunStart(context.Background(), root, "sec", ""); err != nil {
		t.Fatalf("a running org: %v", err)
	}
}
