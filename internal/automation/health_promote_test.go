package automation

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/action"
)

// selectorOrder returns the effective candidate list of key as short strings.
func selectorOrder(t *testing.T, r *Registry, id, key string) []string {
	t.Helper()
	pkg, err := r.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := pkg.Context().Selector(key)
	if !ok {
		t.Fatalf("selector %s missing", key)
	}
	var out []string
	for _, c := range e.Candidates {
		switch {
		case c.CSS != "":
			out = append(out, "css:"+c.CSS)
		case c.Aria != nil:
			out = append(out, "aria:"+c.Aria.Name)
		default:
			out = append(out, "?")
		}
	}
	return out
}

func installAcme(t *testing.T, source string) *Registry {
	t.Helper()
	r := newReg(t)
	if _, err := r.Install(acmeDir(t), InstallOptions{Source: source}); err != nil {
		t.Fatalf("install: %v", err)
	}
	return r
}

func TestHealthPromoteImportedWritesOverlayIdempotently(t *testing.T) {
	r := installAcme(t, SourceImported)
	pkg, _ := r.Get("acme-crm")
	before, err := os.ReadFile(filepath.Join(pkg.Dir, "selectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.PromoteSelector("acme-crm", "contact.email", 1); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "aria:Email" || len(got) != 2 {
		t.Fatalf("after promotion: %v", got)
	}
	after, _ := os.ReadFile(filepath.Join(pkg.Dir, "selectors.json"))
	if string(before) != string(after) {
		t.Fatal("an imported package's own selectors.json must not change; the overlay carries the promotion")
	}
	// Promoting the same candidate again (now first) changes nothing.
	cur, _ := r.Get("acme-crm")
	e, _ := cur.Context().Selector("contact.email")
	if err := r.promoteCandidate(cur, "contact.email", *e, e.Candidates[0]); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "aria:Email" {
		t.Fatalf("re-promotion flipped the order: %v", got)
	}
	// Out-of-range, first and unknown keys are no-ops.
	for _, idx := range []int{0, -1, 7} {
		if err := r.PromoteSelector("acme-crm", "contact.email", idx); err != nil {
			t.Fatalf("idx %d: %v", idx, err)
		}
	}
	if err := r.PromoteSelector("acme-crm", "no.such.key", 1); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "aria:Email" {
		t.Fatalf("no-op promotions changed the order: %v", got)
	}
}

func TestHealthPromoteLocalRewritesPackage(t *testing.T) {
	r := installAcme(t, SourceLocal)
	if err := r.PromoteSelector("acme-crm", "contact.email", 1); err != nil {
		t.Fatal(err)
	}
	pkg, _ := r.Get("acme-crm")
	sels, err := pkg.Selectors()
	if err != nil {
		t.Fatal(err)
	}
	if c := sels["contact.email"].Candidates; len(c) != 2 || c[0].Aria == nil || c[1].CSS != "input[name=email]" {
		t.Fatalf("local package selectors.json not promoted: %+v", c)
	}
	if len(sels) != 4 {
		t.Fatalf("other selector entries lost: %d left", len(sels))
	}
	if _, err := os.Stat(filepath.Join(r.Root(), "acme-crm", "overlay", "selectors.json")); err == nil {
		t.Fatal("a local package must be promoted in place, not through the overlay")
	}
}

func TestHealthRecorderPromotesThroughRegistry(t *testing.T) {
	r := installAcme(t, SourceImported)
	rec := NewHealthRecorder(healthTestDB(t), HealthOptions{Interval: time.Hour, Promoter: r})
	defer rec.Close()
	rec.ObserveSelector("acme-crm", "contact.email", 1, true, true)
	if err := rec.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "aria:Email" {
		t.Fatalf("recorder did not promote: %v", got)
	}
}

func TestMoveCandidateFirst(t *testing.T) {
	a, b, c := action.SelectorCandidate{CSS: "a"}, action.SelectorCandidate{CSS: "b"}, action.SelectorCandidate{Text: "c"}
	e := action.SelectorEntry{Candidates: []action.SelectorCandidate{a, b, c}, Intent: "x"}
	got, changed := moveCandidateFirst(e, c)
	if !changed || got.Candidates[0] != c || got.Candidates[1] != a || got.Candidates[2] != b || got.Intent != "x" {
		t.Fatalf("moveCandidateFirst = %+v %v", got, changed)
	}
	if e.Candidates[0] != a {
		t.Fatal("input entry was mutated")
	}
	if _, changed := moveCandidateFirst(got, c); changed {
		t.Fatal("moving the first candidate again must be a no-op")
	}
}
