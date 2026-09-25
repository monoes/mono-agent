package automation

import (
	"os"
	"path/filepath"
	"sync"
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

var (
	emailCSS  = action.SelectorCandidate{CSS: "input[name=email]"}
	emailAria = action.SelectorCandidate{Aria: &action.AriaSelector{Role: "textbox", Name: "Email"}}
)

func indexEntryOf(t *testing.T, r *Registry, id string) indexEntry {
	t.Helper()
	idx, err := r.readIndex()
	if err != nil {
		t.Fatal(err)
	}
	return *idx.Packages[id]
}

func TestHealthPromoteImportedWritesOverlayIdempotently(t *testing.T) {
	r := installAcme(t, SourceImported)
	pkg, _ := r.Get("acme-crm")
	before, err := os.ReadFile(filepath.Join(pkg.Dir, "selectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.PromoteCandidate("acme-crm", "contact.email", emailAria); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); len(got) != 2 || got[0] != "aria:Email" {
		t.Fatalf("after promotion: %v", got)
	}
	after, _ := os.ReadFile(filepath.Join(pkg.Dir, "selectors.json"))
	if string(before) != string(after) {
		t.Fatal("an imported package's own selectors.json must not change; the overlay carries the promotion")
	}
	// The same candidate again — e.g. a stale report from a run that
	// started before the promotion — changes nothing.
	ov := filepath.Join(r.Root(), "acme-crm", "overlay", "selectors.json")
	ovBefore, _ := os.ReadFile(ov)
	for i := 0; i < 3; i++ {
		if err := r.PromoteCandidate("acme-crm", "contact.email", emailAria); err != nil {
			t.Fatal(err)
		}
	}
	if ovAfter, _ := os.ReadFile(ov); string(ovAfter) != string(ovBefore) {
		t.Fatal("repeating a promotion rewrote the overlay")
	}
	// A candidate not in the entry, and an unknown key, are no-ops.
	if err := r.PromoteCandidate("acme-crm", "contact.email", action.SelectorCandidate{CSS: "#nope"}); err != nil {
		t.Fatal(err)
	}
	if err := r.PromoteCandidate("acme-crm", "no.such.key", emailCSS); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "aria:Email" {
		t.Fatalf("no-op promotions changed the order: %v", got)
	}
	// Promoting the other candidate works on the overlay's order.
	if err := r.PromoteCandidate("acme-crm", "contact.email", emailCSS); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "css:input[name=email]" {
		t.Fatalf("second promotion: %v", got)
	}
	if err := r.PromoteCandidate("not-installed", "k", emailCSS); err == nil {
		t.Fatal("promoting in a package that is not installed should fail")
	}
}

func TestHealthPromoteLocalRewritesPackageAndRefreshesSHA(t *testing.T) {
	r := installAcme(t, SourceLocal)
	shaBefore := indexEntryOf(t, r, "acme-crm").InstalledSha256
	if err := r.PromoteCandidate("acme-crm", "contact.email", emailAria); err != nil {
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
	shaAfter := indexEntryOf(t, r, "acme-crm").InstalledSha256
	want, err := fsHash(os.DirFS(pkg.Dir))
	if err != nil {
		t.Fatal(err)
	}
	if shaAfter == shaBefore || shaAfter != want {
		t.Fatalf("installedSha256 not refreshed: before %s after %s files %s", shaBefore, shaAfter, want)
	}
	// Idempotent: again changes neither files nor index.
	if err := r.PromoteCandidate("acme-crm", "contact.email", emailAria); err != nil {
		t.Fatal(err)
	}
	if got := indexEntryOf(t, r, "acme-crm").InstalledSha256; got != shaAfter {
		t.Fatal("a no-op promotion changed installedSha256")
	}
}

func TestHealthPromoteConcurrentIsSerialised(t *testing.T) {
	r := installAcme(t, SourceLocal)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := emailAria
			if i%2 == 0 {
				c = emailCSS
			}
			if err := r.PromoteCandidate("acme-crm", "contact.email", c); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	pkg, _ := r.Get("acme-crm")
	sels, err := pkg.Selectors()
	if err != nil {
		t.Fatal(err)
	}
	if c := sels["contact.email"].Candidates; len(c) != 2 {
		t.Fatalf("concurrent promotions lost or duplicated candidates: %+v", c)
	}
	want, _ := fsHash(os.DirFS(pkg.Dir))
	if got := indexEntryOf(t, r, "acme-crm").InstalledSha256; got != want {
		t.Fatal("installedSha256 out of step with the files after concurrent promotions")
	}
}

func TestHealthRecorderPromotesThroughRegistry(t *testing.T) {
	r := installAcme(t, SourceImported)
	rec := NewHealthRecorder(healthTestDB(t), HealthOptions{Interval: time.Hour, Promoter: r})
	defer rec.Close()
	c := emailAria
	rec.ObserveSelectorCandidate("acme-crm", "contact.email", &c, 1, true, true)
	if err := rec.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "aria:Email" {
		t.Fatalf("recorder did not promote: %v", got)
	}
	// A stale report of the old index with the same content: no flip back.
	rec.ObserveSelectorCandidate("acme-crm", "contact.email", &c, 1, true, true)
	if err := rec.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "aria:Email" {
		t.Fatalf("stale report flipped the order: %v", got)
	}
}

func TestHealthBootedPromoterUsesBootedRegistry(t *testing.T) {
	prev := action.CurrentDefSource()
	t.Cleanup(func() { action.SetDefSource(prev) })
	action.SetDefSource(nil)
	if bootedRegistry() != nil {
		t.Fatal("no DefSource booted: expected no registry")
	}
	if err := (bootedPromoter{}).PromoteCandidate("acme-crm", "contact.email", emailAria); err != nil {
		t.Fatalf("with nothing booted, promotion must be a silent no-op: %v", err)
	}
	r := installAcme(t, SourceImported)
	action.SetDefSource(r.DefSource())
	if bootedRegistry() != r {
		t.Fatal("bootedRegistry is not the registry behind the DefSource")
	}
	if err := (bootedPromoter{}).PromoteCandidate("acme-crm", "contact.email", emailAria); err != nil {
		t.Fatal(err)
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "aria:Email" {
		t.Fatalf("booted promoter did not promote in the booted registry: %v", got)
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

// A built-in that carries local trust (possible after AddAction) is still
// not the user's own package: it is promoted through the overlay.
func TestHealthPromoteBuiltinWithLocalTrustUsesOverlay(t *testing.T) {
	r := installAcme(t, SourceLocal)
	if err := r.update(func(idx *indexFile) (bool, error) {
		e := idx.Packages["acme-crm"]
		e.Source, e.Trust = SourceBuiltin, TrustLocal
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	pkg, _ := r.Get("acme-crm")
	before, _ := os.ReadFile(filepath.Join(pkg.Dir, "selectors.json"))
	if err := r.PromoteCandidate("acme-crm", "contact.email", emailAria); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(pkg.Dir, "selectors.json")); string(after) != string(before) {
		t.Fatal("a built-in with local trust must not be rewritten in place")
	}
	if got := selectorOrder(t, r, "acme-crm", "contact.email"); got[0] != "aria:Email" {
		t.Fatalf("overlay promotion missing: %v", got)
	}
}
