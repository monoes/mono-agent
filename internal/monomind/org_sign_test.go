package monomind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
)

// monomind advertises no capability for signed org definitions, so the
// gate is the version (2.21.0, monomind#502).
func TestOrgSigningGate(t *testing.T) {
	for v, want := range map[string]bool{"2.21.0": true, "2.21.3": true, "2.22.0": true, "3.0.0": true, "2.20.9": false, "2.21.0-rc.1": true, "": false} {
		if got := NewCapabilitySet(v, "org-json-v1").OrgSigning(); got != want {
			t.Errorf("OrgSigning(%q) = %v, want %v", v, got, want)
		}
	}
	var nilSet *CapabilitySet
	if nilSet.OrgSigning() {
		t.Error("nil set")
	}
}

func TestSignatureRefusalClassified(t *testing.T) {
	for text, want := range map[string]string{
		"org growth: the definition has no operator signature — run `monomind org sign growth`":            orgsign.StateUnsigned,
		"org growth is not signed (changed)":                                                               orgsign.StateChanged,
		"org growth: the definition changed since the operator signed it (policy, roles, runtime, skills)": orgsign.StateChanged,
		"org growth: the definition has an operator signature that does not verify (no operator key)":      orgsign.StateInvalid,
		"org growth: the definition holds a forbidden key (roles[0].__proto__) — remove it":                orgsign.StateForbiddenKey,
		"org growth: budget exceeded": "",
	} {
		if got := signatureRefusalReason(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
	err := asSignatureRefusal("growth", errors.New("monomind org run growth --yes: org growth is not signed (unsigned)"))
	se, ok := AsOrgSignatureError(err)
	if !ok || se.Org != "growth" || se.Status.State != orgsign.StateUnsigned {
		t.Fatalf("err = %v", err)
	}
	plain := errors.New("boom")
	if got := asSignatureRefusal("growth", plain); got != plain {
		t.Fatalf("other errors pass through, got %v", got)
	}
}

// fakeCheckMonomind answers the handshake as version and `org sign <org>
// --check --format json` with body (exit 1), logging each call's cwd+argv.
func fakeCheckMonomind(t *testing.T, version, body string) string {
	t.Helper()
	caps := ""
	if versionAtLeast(version, "2.22.0") {
		caps = `,"` + CapOrgSignCheck + `"`
	}
	return fakeCheckMonomindCaps(t, version, caps, body)
}

// fakeCheckMonomindCaps is fakeCheckMonomind with extra capabilities (a
// JSON fragment starting with a comma, or "").
func fakeCheckMonomindCaps(t *testing.T, version, caps, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "monomind")
	script := "#!/bin/sh\necho \"$(pwd) $*\" >> '" + log + "'\n" +
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + version + `","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"` + caps + `]}'; exit 0; fi` + "\n" +
		"cat <<'JSON'\n" + body + "\nJSON\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOverride, bin)
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)
	return log
}

func TestOrgSignCheck(t *testing.T) {
	root := t.TempDir()
	log := fakeCheckMonomind(t, "2.22.0", `{"orgs":[{"org":"growth","state":"changed","signedAt":"2026-09-30T12:00:00Z","message":"org growth changed"}]}`)
	st, ok := OrgSignCheck(context.Background(), root, "growth")
	if !ok || st.State != orgsign.StateChanged || st.Detail != "org growth changed" {
		t.Fatalf("check = %+v, %v", st, ok)
	}
	calls, _ := os.ReadFile(log)
	real, _ := filepath.EvalSymlinks(root)
	if !strings.Contains(string(calls), real+" org sign growth --check --format json") {
		t.Fatalf("calls:\n%s", calls)
	}

	// A bad HMAC is invalid-signature; "invalid" is a broken definition.
	fakeCheckMonomind(t, "2.22.0", `{"orgs":[{"org":"growth","state":"invalid-signature","message":"bad sig"}]}`)
	if st, ok := OrgSignCheck(context.Background(), root, "growth"); !ok || st.State != orgsign.StateInvalid || !st.Refused() {
		t.Fatalf("invalid-signature = %+v, %v", st, ok)
	}
	fakeCheckMonomind(t, "2.22.0", `{"orgs":[{"org":"growth","state":"invalid","message":"not JSON"}]}`)
	if st, ok := OrgSignCheck(context.Background(), root, "growth"); !ok || st.State != orgsign.StateInvalidDefinition || st.Refused() {
		t.Fatalf("invalid = %+v, %v", st, ok)
	}
	// Anything else — including a 2.22 that ships without --check or with
	// another shape — is no answer, so the Go check is used.
	for _, body := range []string{
		`{"orgs":[{"org":"growth","state":"not-found"}]}`, `not json`, `{"orgs":[{"org":"other","state":"signed"}]}`,
		`[ERROR] Unknown option: --check`, `{"growth":"signed"}`, `{"orgs":[{"org":"growth","state":"verified"}]}`,
	} {
		fakeCheckMonomind(t, "2.22.0", body)
		if _, ok := OrgSignCheck(context.Background(), root, "growth"); ok {
			t.Fatalf("%s: want no verdict", body)
		}
	}
	// No org-sign-check capability — 2.21, or a 2.22 without it — means
	// an older binary: --check is never run.
	for _, version := range []string{"2.21.0", "2.22.0"} {
		log = fakeCheckMonomindCaps(t, version, "", `{"orgs":[{"org":"growth","state":"signed"}]}`)
		if _, ok := OrgSignCheck(context.Background(), root, "growth"); ok {
			t.Fatalf("used --check on %s without the capability", version)
		}
		if calls, _ := os.ReadFile(log); strings.Contains(string(calls), "--check") {
			t.Fatalf("ran --check on %s without the capability:\n%s", version, calls)
		}
	}
}

func TestOrgSignCheckAll(t *testing.T) {
	root := t.TempDir()
	log := fakeCheckMonomind(t, "2.22.0", `{"orgs":[{"org":"a","state":"signed"},{"org":"b","state":"changed"},{"org":"c","state":"not-found"}]}`)
	got, ok := OrgSignCheckAll(context.Background(), root)
	if !ok || len(got) != 2 || got["a"].State != orgsign.StateSigned || got["b"].State != orgsign.StateChanged {
		t.Fatalf("all = %+v, %v", got, ok)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "org sign --all --check --format json") {
		t.Fatalf("calls:\n%s", calls)
	}
}

// --expect-hash is passed only when the pinned monomind advertises
// org-sign-expect-hash and its help lists the flag. Help alone (without
// the capability) is never enough: 2.21 ignores an unknown flag.
func TestOrgSignExpectHashOnlyWhenOffered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	for _, c := range []struct {
		version, caps, help string
		want                bool
	}{
		{"2.22.0", `,"org-sign-expect-hash"`, "  --expect-hash <hex>  Sign only if the signable hash is <hex>", true},
		{"2.22.0", `,"org-sign-expect-hash"`, "  --yes  Skip the confirmation", false},
		{"2.22.0", "", "  --expect-hash <hex>", false},
		{"2.21.0", "", "  --expect-hash <hex>", false},
	} {
		dir := t.TempDir()
		log := filepath.Join(dir, "calls")
		bin := filepath.Join(dir, "monomind")
		script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" +
			`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + c.version + `","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"` + c.caps + `]}'; exit 0; fi` + "\n" +
			`if [ "$3" = "--help" ]; then echo '` + c.help + `'; exit 0; fi` + "\n" +
			"echo signed\nexit 0\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvOverride, bin)
		ResetCapabilityCache()
		if _, err := OrgSign(context.Background(), t.TempDir(), "growth", "abc123"); err != nil {
			t.Fatal(err)
		}
		calls, _ := os.ReadFile(log)
		got := strings.Contains(string(calls), "org sign growth --yes --expect-hash abc123")
		if got != c.want || !strings.Contains(string(calls), "org sign growth --yes") {
			t.Errorf("%s caps %q help %q: calls\n%s", c.version, c.caps, c.help, calls)
		}
	}
	ResetCapabilityCache()
}

// The review JSON, used only when the pinned monomind advertises
// org-sign-review-json: the reviewText and the hash of what was reviewed.
// Any other shape, or no capability, gives the human review instead.
func TestOrgSignReviewJSON(t *testing.T) {
	root := t.TempDir()
	h := strings.Repeat("ab", 32)
	const cap = `,"org-sign-review-json"`
	for _, c := range []struct {
		version, caps, jsonOut, wantHash, wantText string
		wantErr                                    bool
	}{
		{"2.22.0", cap, `{"org":"growth","state":"changed","hash":"` + strings.ToUpper(h) + `","review":{"authority":[]},"reviewText":"org growth (changed):"}`, h, "org growth (changed):", false},
		{"2.22.0", cap, `{"org":"growth","error":"org not found: growth"}`, "", "", true},
		{"2.22.0", cap, `{"org":"growth","state":"changed","reviewText":"no hash here"}`, "", "text review", false},
		{"2.22.0", cap, `{"org":"growth","hash":"xyz","reviewText":"bad hash"}`, "", "text review", false},
		{"2.22.0", "", `{"org":"growth","hash":"` + h + `","reviewText":"no capability"}`, "", "text review", false},
		{"2.21.0", "", `{"org":"growth","hash":"` + h + `","reviewText":"never read"}`, "", "text review", false},
	} {
		dir := t.TempDir()
		bin := filepath.Join(dir, "monomind")
		script := "#!/bin/sh\n" +
			`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + c.version + `","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"` + c.caps + `]}'; exit 0; fi` + "\n" +
			`if [ "$4" = "--format" ]; then echo '` + c.jsonOut + `'; exit 0; fi` + "\n" +
			"echo 'text review'\necho 'Not signed. Review the above, then sign it yourself in a terminal: monomind org sign <org> (or pass --yes).'\nexit 1\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvOverride, bin)
		ResetCapabilityCache()
		rv, err := OrgSignReview(context.Background(), root, "growth")
		if (err != nil) != c.wantErr || rv.Hash != c.wantHash || rv.Text != c.wantText {
			t.Errorf("%s caps %q %s: %+v, %v", c.version, c.caps, c.jsonOut, rv, err)
		}
	}
	ResetCapabilityCache()
}

// The capabilities may sit at the top of the handshake or under "org".
func TestHandshakeInReadsCapabilitiesInEitherPlace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	for _, out := range []string{
		`{"v":1,"version":"2.22.0","capabilities":["org-sign-expect-hash"]}`,
		`{"v":1,"version":"2.22.0","capabilities":["agent-exec"],"org":{"capabilities":["org-sign-expect-hash"]}}`,
	} {
		bin := filepath.Join(t.TempDir(), "monomind")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, caps, err := handshakeIn(context.Background(), bin, t.TempDir())
		if err != nil || !caps[CapOrgSignExpectHash] {
			t.Errorf("%s: caps %v, %v", out, caps, err)
		}
	}
}

// A version shim (mise here) picks the monomind per project: it is
// resolved in the project root (`mise which monomind`) and the handshake,
// the help and the sign all run through that exact binary, in that root.
// The shim itself is never run for them.
func TestSignToolPinsTheBinaryAShimPicks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake binaries are shell scripts")
	}
	base := t.TempDir()
	log := filepath.Join(base, "calls")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(base, "installs", "2.21", "monomind")
	cur := filepath.Join(base, "installs", "2.22", "monomind")
	fake := func(name, version, caps, help string) string {
		return `echo "` + name + ` $(pwd) $*" >> '` + log + "'\n" +
			`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + version + `","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"` + caps + `]}'; exit 0; fi` + "\n" +
			`if [ "$3" = "--help" ]; then echo '` + help + `'; exit 0; fi` + "\n" +
			"echo signed\n"
	}
	write(old, fake("OLD", "2.21.0", "", "  --yes"))
	write(cur, fake("CUR", "2.22.0", `,"org-sign-expect-hash"`, "  --expect-hash <hex>"))
	shim := filepath.Join(base, "mise", "shims", "monomind")
	write(shim, `echo "SHIM $*" >> '`+log+"'\nexit 3\n")
	write(filepath.Join(base, "bin", "mise"),
		`if [ "$1" = "which" ] && [ -f .tool-versions ]; then echo '`+old+`'; exit 0; fi`+"\n"+
			`if [ "$1" = "which" ]; then echo '`+cur+`'; exit 0; fi`+"\nexit 1\n")
	t.Setenv("PATH", filepath.Join(base, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(EnvOverride, shim)
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)

	pinned, plain := t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(pinned, ".tool-versions"), []byte("monomind 2.21.0\n"), 0o644)
	ctx := context.Background()
	if OrgSignExpectsHash(ctx, pinned) {
		t.Error("claimed --expect-hash where the shim picks 2.21")
	}
	if !OrgSignExpectsHash(ctx, plain) {
		t.Error("missed --expect-hash where the shim picks 2.22")
	}
	for _, root := range []string{pinned, plain} {
		if _, err := OrgSign(ctx, root, "growth", "abc123"); err != nil {
			t.Fatal(err)
		}
	}
	calls, _ := os.ReadFile(log)
	realPinned, _ := filepath.EvalSymlinks(pinned)
	realPlain, _ := filepath.EvalSymlinks(plain)
	for _, want := range []string{
		"OLD " + realPinned + " org sign growth --yes\n",
		"CUR " + realPlain + " org sign growth --yes --expect-hash abc123\n",
	} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("missing %q in calls:\n%s", want, calls)
		}
	}
	if strings.Contains(string(calls), "SHIM") {
		t.Errorf("the shim itself was run:\n%s", calls)
	}

	// A shim that can't be resolved signs nothing.
	write(filepath.Join(base, "bin", "mise"), "exit 1\n")
	ResetCapabilityCache()
	if _, err := OrgSign(ctx, plain, "growth", "abc123"); err == nil {
		t.Error("signed through an unresolvable shim")
	}
}

// OrgSigningEnforced asks the monomind that runs in each project: behind a
// shim that picks 2.20 globally and 2.21 in one project, only that project
// enforces (and is re-signed). The handshake runs once per (binary, root).
func TestOrgSigningEnforcedPerProjectRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake binaries are shell scripts")
	}
	base := t.TempDir()
	log := filepath.Join(base, "calls")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake := func(name, version string) string {
		return `echo "` + name + ` $*" >> '` + log + "'\n" +
			`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + version + `","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'; exit 0; fi` + "\n"
	}
	global := filepath.Join(base, "installs", "2.20", "monomind")
	project := filepath.Join(base, "installs", "2.21", "monomind")
	write(global, fake("G220", "2.20.3"))
	write(project, fake("P221", "2.21.0"))
	shim := filepath.Join(base, "mise", "shims", "monomind")
	write(shim, "exit 3\n")
	write(filepath.Join(base, "bin", "mise"),
		`if [ -f .tool-versions ]; then echo '`+project+`'; else echo '`+global+`'; fi`+"\n")
	t.Setenv("PATH", filepath.Join(base, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(EnvOverride, shim)
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)

	onProject, plain := t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(onProject, ".tool-versions"), []byte("monomind 2.21.0\n"), 0o644)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if !OrgSigningEnforced(ctx, onProject) {
			t.Fatal("the project on 2.21 does not enforce")
		}
		if OrgSigningEnforced(ctx, plain) {
			t.Fatal("the global 2.20 enforces")
		}
	}
	calls, _ := os.ReadFile(log)
	if strings.Count(string(calls), "P221 --version") != 1 || strings.Count(string(calls), "G220 --version") != 1 {
		t.Fatalf("handshakes not cached per root:\n%s", calls)
	}
}
