package automation

import (
	"path/filepath"
	"testing"
)

// FoldLegacy wraps ~/.monoagent/actions/<p> into local-<p> without seeding
// anything, is a no-op until a legacy directory changes, and takes a
// platform's native bot and site from the installed package that owns it.
func TestFoldLegacyWithoutSeeds(t *testing.T) {
	r := newReg(t)
	writeLegacyDirs(t, r.Home())
	rep, err := r.FoldLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Installed)+len(rep.Updated) != 0 {
		t.Fatalf("FoldLegacy seeded packages: %+v", rep)
	}
	if by := legacyByAlias(t, r); len(by) != 4 {
		t.Fatalf("legacy packages by alias: %v", by)
	}
	if rep, _ := r.FoldLegacy(); rep.Changed() {
		t.Fatalf("second fold changed something: %+v", rep)
	}
	writeTree(t, filepath.Join(r.Home(), "actions", "devtool"), map[string]string{
		"extra.json": `{"actionType":"extra","steps":[{"id":"w","type":"wait","duration":1}]}`,
	})
	if rep, _ := r.FoldLegacy(); len(rep.LegacyWrapped) != 1 {
		t.Fatalf("changed legacy dir not refolded: %+v", rep)
	}
}

func TestFoldLegacyUsesInstalledPackageMetadata(t *testing.T) {
	r := newReg(t)
	if err := r.Seed(seedFS("1.0.0", "a")); err != nil { // an installed "demo"
		t.Fatal(err)
	}
	writeTree(t, filepath.Join(r.Home(), "actions", "demo"), map[string]string{
		"extra.json": `{"actionType":"extra","steps":[{"id":"w","type":"wait","duration":1}]}`,
	})
	if _, err := r.FoldLegacy(); err != nil {
		t.Fatal(err)
	}
	by := legacyByAlias(t, r)
	if _, ok := by["demo"]; !ok {
		t.Fatalf("legacy demo not wrapped: %v", by)
	}
	seeds := func() []seedPkg {
		idx, _ := r.readIndex()
		return r.installedSeedsLocked(idx)
	}()
	for _, s := range seeds {
		if s.pkg.Manifest.Legacy != nil {
			t.Fatalf("installedSeedsLocked returned the generated legacy package %s", s.pkg.Manifest.ID)
		}
	}
	if len(seeds) != 1 || seeds[0].pkg.Manifest.ID != "demo" {
		t.Fatalf("installed seeds = %d", len(seeds))
	}
}

// Boot seeds TestSeed only when a test set it.
func TestBootSeedsOnlyUnderTests(t *testing.T) {
	prev := TestSeed
	t.Cleanup(func() { TestSeed = prev })
	TestSeed = nil
	r := newReg(t)
	if _, err := r.Boot(); err != nil {
		t.Fatal(err)
	}
	if infos, _ := r.List(true); len(infos) != 0 {
		t.Fatalf("Boot without TestSeed installed %d packages", len(infos))
	}
	TestSeed = seedFS("1.0.0", "a")
	if rep, err := r.Boot(); err != nil || len(rep.Installed) != 1 {
		t.Fatalf("Boot with TestSeed: %+v, %v", rep, err)
	}
}
