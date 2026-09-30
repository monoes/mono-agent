package orgsign

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeSigner signs whatever is on disk when called, as `monomind org sign
// --yes` does; before runs first (to simulate a concurrent writer).
type fakeSigner struct {
	t      *testing.T
	calls  int
	before func()
	err    error
}

func (f *fakeSigner) Sign(_ context.Context, root, org string) error {
	f.calls++
	if f.before != nil {
		f.before()
	}
	if f.err != nil {
		return f.err
	}
	raw, _, err := ReadFile(root, org)
	if err != nil {
		return err
	}
	signFixture(f.t, root, org, raw)
	return nil
}

func writeOrg(t *testing.T, root, org, body string) string {
	t.Helper()
	dir := filepath.Join(root, ".monomind", "orgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, org+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return SHA256([]byte(body))
}

const (
	signedBody = `{"name":"growth","roles":[{"id":"lead","policy":{"git":"read"}}]}`
	ownBody    = `{"name":"growth","roles":[{"id":"lead","policy":{"git":"read"}}],"autonomy":{"level":"low"}}`
	evilBody   = `{"name":"growth","roles":[{"id":"lead","policy":{"git":"push"}}]}`
)

func TestVerifiedBeforeWriteIsSignedAfter(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	loaded := writeOrg(t, root, "growth", signedBody)
	signFixture(t, root, "growth", []byte(signedBody))

	pre := Before(context.Background(), nil, root, "growth", loaded, false)
	if !pre.Eligible() {
		t.Fatalf("a verified file loaded as is must be eligible: %+v", pre)
	}
	sha := writeOrg(t, root, "growth", ownBody)
	s := &fakeSigner{t: t}
	out := pre.After(context.Background(), s, "growth", sha)
	if !out.Signed || out.Notice != "" || s.calls != 1 {
		t.Fatalf("outcome %+v, sign calls %d", out, s.calls)
	}
	if st, _, _ := VerifyFile(root, "growth"); !st.OK() {
		t.Fatalf("after: %+v", st)
	}
}

func TestTamperedBeforeWriteIsNotSigned(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	writeOrg(t, root, "growth", signedBody)
	signFixture(t, root, "growth", []byte(signedBody))
	// Something outside mono-agent widens the policy; mono-agent then loads
	// the file (with that edit) and makes its own change.
	loaded := writeOrg(t, root, "growth", evilBody)

	pre := Before(context.Background(), nil, root, "growth", loaded, false)
	if pre.Eligible() {
		t.Fatal("a changed file must not be eligible")
	}
	sha := writeOrg(t, root, "growth", strings.Replace(evilBody, `]}`, `],"autonomy":{"level":"low"}}`, 1))
	s := &fakeSigner{t: t}
	out := pre.After(context.Background(), s, "growth", sha)
	if out.Signed || s.calls != 0 {
		t.Fatalf("signed an outside edit: %+v (calls %d)", out, s.calls)
	}
	if out.State != StateChanged || !strings.Contains(out.Notice, "monoagentcli org sign growth") {
		t.Fatalf("notice: %+v", out)
	}
}

// An outside edit that is reverted after mono-agent loaded the file must
// not ride along: the document no longer matches the verified bytes.
func TestDocLoadedFromOtherBytesIsNotSigned(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	writeOrg(t, root, "growth", signedBody)
	signFixture(t, root, "growth", []byte(signedBody))
	loadedEvil := writeOrg(t, root, "growth", evilBody) // mono-agent loads this
	writeOrg(t, root, "growth", signedBody)             // then it is reverted

	if pre := Before(context.Background(), nil, root, "growth", loadedEvil, false); pre.Eligible() {
		t.Fatal("eligible although the document came from other bytes")
	}
	if pre := Before(context.Background(), nil, root, "growth", "", false); pre.Eligible() {
		t.Fatal("eligible for a document not loaded from the file")
	}
}

func TestNewOrgSignedOnlyWhenOwnContent(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	if Before(context.Background(), nil, root, "growth", "", false).Eligible() {
		t.Fatal("an imported new org must not be signed")
	}
	pre := Before(context.Background(), nil, root, "growth", "", true)
	if !pre.Eligible() {
		t.Fatal("a new org mono-agent wrote itself should be signed")
	}
	sha := writeOrg(t, root, "growth", signedBody)
	if out := pre.After(context.Background(), &fakeSigner{t: t}, "growth", sha); !out.Signed {
		t.Fatalf("outcome %+v", out)
	}
}

func TestRaceDuringSignWithdrawsSignature(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	loaded := writeOrg(t, root, "growth", signedBody)
	signFixture(t, root, "growth", []byte(signedBody))
	pre := Before(context.Background(), nil, root, "growth", loaded, false)
	sha := writeOrg(t, root, "growth", ownBody)
	s := &fakeSigner{t: t, before: func() { writeOrg(t, root, "growth", evilBody) }}
	out := pre.After(context.Background(), s, "growth", sha)
	if out.Signed || !strings.Contains(out.Notice, "withdrawn") {
		t.Fatalf("outcome %+v", out)
	}
	if st, _, _ := VerifyFile(root, "growth"); st.OK() {
		t.Fatal("the other writer's content is signed")
	}
}

func TestFileChangedAfterWriteIsNotSigned(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	loaded := writeOrg(t, root, "growth", signedBody)
	signFixture(t, root, "growth", []byte(signedBody))
	pre := Before(context.Background(), nil, root, "growth", loaded, false)
	sha := writeOrg(t, root, "growth", ownBody)
	writeOrg(t, root, "growth", evilBody)
	s := &fakeSigner{t: t}
	if out := pre.After(context.Background(), s, "growth", sha); out.Signed || s.calls != 0 {
		t.Fatalf("outcome %+v (calls %d)", out, s.calls)
	}
}

func TestSignFailureAndRoleContext(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	loaded := writeOrg(t, root, "growth", signedBody)
	signFixture(t, root, "growth", []byte(signedBody))
	pre := Before(context.Background(), nil, root, "growth", loaded, false)
	sha := writeOrg(t, root, "growth", ownBody)
	out := pre.After(context.Background(), &fakeSigner{t: t, err: errors.New("refused")}, "growth", sha)
	if out.Signed || !strings.Contains(out.Notice, "refused") {
		t.Fatalf("outcome %+v", out)
	}

	t.Setenv("MONOMIND_AGENT_EXEC", "1")
	s := &fakeSigner{t: t}
	if Before(context.Background(), nil, root, "growth", sha, false).Eligible() {
		t.Fatal("eligible inside an agent turn")
	}
	if out := SignExact(context.Background(), s, root, "growth", sha); out.Signed || s.calls != 0 {
		t.Fatalf("signed inside an agent turn: %+v", out)
	}
}

func TestCosmeticWriteNeedsNoSignature(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	loaded := writeOrg(t, root, "growth", signedBody)
	signFixture(t, root, "growth", []byte(signedBody))
	pre := Before(context.Background(), nil, root, "growth", loaded, false)
	sha := writeOrg(t, root, "growth", `{"name":"growth","goal":"new goal","roles":[{"id":"lead","ui":{"x":5},"policy":{"git":"read"}}]}`)
	s := &fakeSigner{t: t}
	if out := pre.After(context.Background(), s, "growth", sha); !out.Signed || s.calls != 0 {
		t.Fatalf("outcome %+v (calls %d)", out, s.calls)
	}
}

// checkingSigner is fakeSigner with monomind 2.22's --check.
type checkingSigner struct {
	fakeSigner
	st     Status
	ok     bool
	checks int
	during func()
}

func (c *checkingSigner) Check(context.Context, string, string) (Status, bool) {
	c.checks++
	if c.during != nil {
		c.during()
	}
	return c.st, c.ok
}

// monomind's own verdict (2.22 --check) wins over the Go check; without
// one, the Go check decides.
func TestBeforePrefersMonomindCheck(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	loaded := writeOrg(t, root, "growth", signedBody) // no sidecar: Go says unsigned

	c := &checkingSigner{fakeSigner: fakeSigner{t: t}, st: Status{State: StateSigned}, ok: true}
	if !Before(context.Background(), c, root, "growth", loaded, false).Eligible() || c.checks != 1 {
		t.Fatalf("monomind said signed; checks %d", c.checks)
	}
	c = &checkingSigner{fakeSigner: fakeSigner{t: t}, st: Status{State: StateChanged}, ok: true}
	signFixture(t, root, "growth", []byte(signedBody)) // Go now says signed
	if Before(context.Background(), c, root, "growth", loaded, false).Eligible() {
		t.Fatal("monomind said changed")
	}
	c = &checkingSigner{fakeSigner: fakeSigner{t: t}, ok: false}
	if !Before(context.Background(), c, root, "growth", loaded, false).Eligible() {
		t.Fatal("without monomind's answer the Go check should decide")
	}
	// A file that changes while monomind checks it gets no verdict.
	c = &checkingSigner{fakeSigner: fakeSigner{t: t}, st: Status{State: StateSigned}, ok: true,
		during: func() { writeOrg(t, root, "growth", evilBody) }}
	if Before(context.Background(), c, root, "growth", loaded, false).Eligible() {
		t.Fatal("eligible although the file changed during the check")
	}
}

// An operator key this process can't read gives no verdict (unknown), so
// nothing is signed on its strength and nothing is refused on it either.
func TestUnreadableKeyIsUnknown(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	operatorDirForTest(t)
	root := t.TempDir()
	loaded := writeOrg(t, root, "growth", signedBody)
	signFixture(t, root, "growth", []byte(signedBody))
	key := filepath.Join(OperatorDir(), "full-access-grant.key")
	if err := os.Chmod(key, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(key, 0o600) })
	st := Verify(root, "growth", []byte(signedBody))
	if st.State != StateUnknown || st.Refused() {
		t.Fatalf("state %+v, want unknown", st)
	}
	if Before(context.Background(), nil, root, "growth", loaded, false).Eligible() {
		t.Fatal("eligible without a verdict")
	}
}
