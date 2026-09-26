package automation

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/monoes/mono-agent/data"
)

func seedFS(version, body string) fstest.MapFS {
	return fstest.MapFS{
		"automations/demo/automation.json":    {Data: []byte(`{"schema":"monoagent.automation/v1","id":"demo","name":"Demo","version":"` + version + `","actions":["hello"],"site":{"domains":[]},"permissions":{"steps":[]},"policy":{"tier":"standard"}}`)},
		"automations/demo/actions/hello.json": {Data: []byte(`{"actionType":"hello","sideEffects":"none","steps":[{"id":"a","type":"log","value":"` + body + `"}]}`)},
		"automations/README.md":               {Data: []byte("not a package")},
	}
}

func indexMod(t *testing.T, r *Registry) time.Time {
	t.Helper()
	st, err := os.Stat(filepath.Join(r.Root(), indexName))
	if err != nil {
		t.Fatal(err)
	}
	return st.ModTime()
}

func TestSeedEmbeddedBuiltins(t *testing.T) {
	r := newReg(t)
	rep, err := r.SeedWithReport(data.AutomationsFS)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(rep.Installed) == 0 {
		t.Fatal("no built-ins installed")
	}
	infos, err := r.List(false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]InstalledInfo{}
	for _, i := range infos {
		byID[i.ID] = i
	}
	hn, ok := byID["hackernews"]
	if !ok || hn.Source != SourceBuiltin || hn.Trust != SourceBuiltin || !hn.Enabled || hn.Actions == 0 || hn.Modified {
		t.Fatalf("hackernews row: %+v", hn)
	}
	// Social built-ins follow the bot gate: unavailable (not an error) when
	// social support is not compiled in.
	ig := byID["instagram"]
	if ig.Available != socialBuild() {
		t.Errorf("instagram available=%v, social build=%v (%s)", ig.Available, socialBuild(), ig.UnavailableReason)
	}
	if !socialBuild() && ig.UnavailableReason == "" {
		t.Error("blocked built-in has no reason")
	}
	if g := byID["gemini"]; !g.Available {
		t.Errorf("gemini should be available: %s", g.UnavailableReason)
	}

	// Second seed: nothing to do, nothing written.
	before := indexMod(t, r)
	time.Sleep(20 * time.Millisecond)
	rep, err = r.SeedWithReport(data.AutomationsFS)
	if err != nil || rep.Changed() {
		t.Fatalf("re-seed changed=%v err=%v %+v", rep.Changed(), err, rep)
	}
	if !indexMod(t, r).Equal(before) {
		t.Error("re-seed rewrote index.json")
	}

	// DefSource lists only enabled + available packages.
	ds := r.DefSource()
	list, err := ds.List()
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, e := range list {
		has[e] = true
	}
	if !has["gemini/generate_text"] {
		t.Error("gemini/generate_text missing from DefSource")
	}
	if has["instagram/like_posts"] != socialBuild() {
		t.Errorf("instagram in DefSource = %v, social build = %v", has["instagram/like_posts"], socialBuild())
	}
	if b, err := ds.Load("GEMINI", "generate_text"); err != nil || len(b) == 0 {
		t.Errorf("Load: %v", err)
	}
	if ds.Package("gemini") == nil {
		t.Error("Package(gemini) nil")
	}
}

func TestSeedRules(t *testing.T) {
	r := newReg(t)
	if err := r.Seed(seedFS("1.0.0", "v1")); err != nil {
		t.Fatal(err)
	}

	// Dev build: same version, changed files, unmodified install → refreshed.
	rep, err := r.SeedWithReport(seedFS("1.0.0", "v1b"))
	if err != nil || len(rep.Refreshed) != 1 {
		t.Fatalf("refresh: %+v %v", rep, err)
	}
	p, _ := r.Get("demo")
	if b, _ := fs.ReadFile(p.FS, "actions/hello.json"); !contains([]string{string(b)}, string(seedFS("1.0.0", "v1b")["automations/demo/actions/hello.json"].Data)) {
		t.Errorf("not refreshed: %s", b)
	}

	// Newer seed, unmodified → updated, previous kept.
	rep, err = r.SeedWithReport(seedFS("1.1.0", "v2"))
	if err != nil || len(rep.Updated) != 1 {
		t.Fatalf("update: %+v %v", rep, err)
	}
	info, _ := r.Info("demo")
	if info.Version != "1.1.0" || info.PreviousVersion != "1.0.0" {
		t.Fatalf("after update: %+v", info)
	}

	// Older seed never downgrades.
	if rep, _ := r.SeedWithReport(seedFS("1.0.5", "old")); rep.Changed() {
		t.Errorf("downgrade seeded: %+v", rep)
	}

	// User edits the installed copy → a newer seed is held back.
	os.WriteFile(filepath.Join(info.Dir, "actions", "hello.json"), []byte(`{"actionType":"hello","steps":[]}`), 0o644)
	if info, _ := r.Info("demo"); !info.Modified {
		t.Error("edit not reported as Modified")
	}
	rep, err = r.SeedWithReport(seedFS("1.2.0", "v3"))
	if err != nil || len(rep.Pending) != 1 {
		t.Fatalf("pending: %+v %v", rep, err)
	}
	info, _ = r.Info("demo")
	if info.Version != "1.1.0" || info.PendingUpdate != "1.2.0" {
		t.Fatalf("user copy not kept: %+v", info)
	}
	// Pending is recorded once; the next seed is a no-op.
	if rep, _ := r.SeedWithReport(seedFS("1.2.0", "v3")); rep.Changed() {
		t.Errorf("pending re-recorded: %+v", rep)
	}

	// Restore discards the edit and installs the shipped copy.
	if err := r.Restore("demo", seedFS("1.2.0", "v3")); err != nil {
		t.Fatal(err)
	}
	info, _ = r.Info("demo")
	if info.Version != "1.2.0" || info.PendingUpdate != "" || info.Modified {
		t.Fatalf("after restore: %+v", info)
	}

	// Uninstall sticks across seeds; Restore brings it back.
	if err := r.Uninstall("demo"); err != nil {
		t.Fatal(err)
	}
	if rep, _ := r.SeedWithReport(seedFS("9.0.0", "v9")); rep.Changed() {
		t.Errorf("removed built-in re-seeded: %+v", rep)
	}
	if _, err := r.Get("demo"); err == nil {
		t.Error("removed package still gettable")
	}
	all, _ := r.List(true)
	if len(all) != 1 || !all[0].Removed {
		t.Errorf("List(all) = %+v", all)
	}
	if l, _ := r.List(false); len(l) != 0 {
		t.Errorf("List() shows removed: %+v", l)
	}
	if err := r.Restore("demo", seedFS("9.0.0", "v9")); err != nil {
		t.Fatal(err)
	}
	if info, err := r.Info("demo"); err != nil || info.Version != "9.0.0" || !info.Enabled {
		t.Fatalf("after restore: %+v %v", info, err)
	}
}

func TestRollbackIsNotUndoneBySeed(t *testing.T) {
	r := newReg(t)
	r.Seed(seedFS("1.0.0", "a"))
	r.Seed(seedFS("1.1.0", "b"))
	if err := r.Rollback("demo"); err != nil {
		t.Fatal(err)
	}
	info, _ := r.Info("demo")
	if info.Version != "1.0.0" || info.PreviousVersion != "1.1.0" {
		t.Fatalf("rollback: %+v", info)
	}
	rep, _ := r.SeedWithReport(seedFS("1.1.0", "b"))
	info, _ = r.Info("demo")
	if info.Version != "1.0.0" || len(rep.Updated) != 0 {
		t.Fatalf("seed undid the rollback: %+v %+v", info, rep)
	}
	// Roll forward again.
	if err := r.Rollback("demo"); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.Info("demo"); info.Version != "1.1.0" {
		t.Fatalf("roll forward: %+v", info)
	}
}

func TestLegacyActionsWrappedOnce(t *testing.T) {
	r := newReg(t)
	legacy := filepath.Join(r.Home(), "actions", "acme")
	writeTree(t, legacy, map[string]string{
		"list_things.json": `{"actionType":"list_things","platform":"acme","steps":[{"id":"a","type":"log"}]}`,
		"bad.json":         `{nope`,
	})
	writeTree(t, filepath.Join(r.Home(), "actions", "instagram"), map[string]string{
		"custom.json": `{"actionType":"custom","steps":[]}`,
	})
	rep, err := r.SeedWithReport(seedFS("1.0.0", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.LegacyWrapped) != 2 {
		t.Fatalf("wrapped %v", rep.LegacyWrapped)
	}
	p, err := r.Get("local-acme")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != SourceLocal || len(p.Manifest.Actions) != 1 || p.Manifest.Actions[0] != "list_things" {
		t.Errorf("local-acme: %+v", p.Manifest)
	}
	if _, err := os.Stat(filepath.Join(legacy, "list_things.json")); err != nil {
		t.Error("legacy file deleted")
	}
	ig, _ := r.Get("local-instagram")
	if ig == nil || ig.Manifest.Requires.Native != "" && ig.Manifest.Requires.Native != "instagram" {
		t.Errorf("local-instagram native: %+v", ig)
	}
	// Once only: deleting the package does not re-wrap it.
	r.Uninstall("local-acme")
	if rep, _ := r.SeedWithReport(seedFS("1.0.0", "a")); len(rep.LegacyWrapped) != 0 {
		t.Errorf("wrapped twice: %+v", rep)
	}
}

func TestSeedAllBuiltinsIncludingX(t *testing.T) {
	sub, err := fs.Sub(data.AutomationsFS, "automations")
	if err != nil {
		t.Fatal(err)
	}
	// Both the raw embed FS and the fs.Sub'd root (as startup passes it).
	for name, builtins := range map[string]fs.FS{"embed": data.AutomationsFS, "sub": sub} {
		t.Run(name, func(t *testing.T) {
			r := newReg(t)
			rep, err := r.SeedWithReport(builtins)
			if err != nil || len(rep.Skipped) != 0 {
				t.Fatalf("seed: %v skipped=%v", err, rep.Skipped)
			}
			for _, id := range []string{"gemini", "hackernews", "instagram", "linkedin", "producthunt", "tiktok", "x"} {
				if _, err := r.Info(id); err != nil {
					t.Errorf("%s not seeded: %v", id, err)
				}
			}
		})
	}
}

func TestSingleCharIDValid(t *testing.T) {
	for _, id := range []string{"x", "7", "a-b"} {
		if !ValidID(id) {
			t.Errorf("%q rejected", id)
		}
	}
	for _, id := range []string{"", "-x", "X", "a_b", strings.Repeat("a", 42)} {
		if ValidID(id) {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestBrokenBuiltinIsSkippedNotFatal(t *testing.T) {
	builtins := seedFS("1.0.0", "a")
	builtins["automations/broken/automation.json"] = &fstest.MapFile{Data: []byte(`{"id":"Broken!","version":"x"}`)}
	r := newReg(t)
	rep, err := r.SeedWithReport(builtins)
	if err != nil {
		t.Fatalf("broken built-in failed the seed: %v", err)
	}
	if len(rep.Skipped) != 1 || len(rep.Installed) != 1 {
		t.Fatalf("report: %+v", rep)
	}
}
