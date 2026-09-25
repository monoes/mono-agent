package recordanalyze

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/automation"
)

// hackernewsSeed returns the shipped hackernews package as a builtins FS,
// optionally with its manifest version replaced.
func hackernewsSeed(t *testing.T, version string) fs.FS {
	t.Helper()
	sub, err := fs.Sub(data.AutomationsFS, "automations")
	if err != nil {
		t.Fatal(err)
	}
	m := fstest.MapFS{}
	err = fs.WalkDir(sub, "hackernews", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		if p == "hackernews/automation.json" && version != "" {
			var mf map[string]any
			if err := json.Unmarshal(b, &mf); err != nil {
				return err
			}
			mf["version"] = version
			b, _ = json.MarshalIndent(mf, "", "  ")
		}
		m[p] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func seedVersion(t *testing.T, seed fs.FS) string {
	var mf automation.Manifest
	b, _ := fs.ReadFile(seed, "hackernews/automation.json")
	if err := json.Unmarshal(b, &mf); err != nil {
		t.Fatal(err)
	}
	return mf.Version
}

// TestSaveActionIntoBuiltin: saving a recorded action into a built-in keeps
// its lineage: version <seed>+local.1, restore brings the seed back, and a
// newer seed is held as pending while the local copy stays.
func TestSaveActionIntoBuiltin(t *testing.T) {
	home := t.TempDir()
	reg := openReg(t, home)
	seed := hackernewsSeed(t, "")
	if err := reg.Seed(seed); err != nil {
		t.Fatal(err)
	}
	base := seedVersion(t, seed)
	dir := draftFrom(t, home, "form-submit", answer(t, "form-submit"))

	res, err := Save(context.Background(), reg, dir, SaveOptions{Automation: "hackernews"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != base+"+local.1" {
		t.Fatalf("version = %q, want %s+local.1", res.Version, base)
	}
	info, _ := reg.Info("hackernews")
	if info.Source != automation.SourceBuiltin || !info.Modified {
		t.Errorf("info = %+v", info)
	}

	// A newer seed does not overwrite the local copy; it is pending.
	next := hackernewsSeed(t, "9.9.9")
	if err := reg.Seed(next); err != nil {
		t.Fatal(err)
	}
	info, _ = reg.Info("hackernews")
	if !strings.HasPrefix(info.Version, base+"+local.") || info.PendingUpdate != "9.9.9" {
		t.Errorf("after newer seed: version %q pending %q", info.Version, info.PendingUpdate)
	}

	// Restore brings back the shipped copy.
	if err := reg.Restore("hackernews", next); err != nil {
		t.Fatal(err)
	}
	info, _ = reg.Info("hackernews")
	if info.Version != "9.9.9" || info.Modified {
		t.Errorf("after restore: %+v", info)
	}
}

// TestSaveFragmentIntoBuiltin: the fragment path goes through AddFragment,
// so a built-in target gets <seed>+local.1 (not a patch bump that would
// collide with the next seed) and stays a built-in.
func TestSaveFragmentIntoBuiltin(t *testing.T) {
	home := t.TempDir()
	reg := openReg(t, home)
	seed := hackernewsSeed(t, "")
	if err := reg.Seed(seed); err != nil {
		t.Fatal(err)
	}
	base := seedVersion(t, seed)
	dir := draftFrom(t, home, "form-submit", answer(t, "form-submit"))
	res, err := Save(context.Background(), reg, dir, SaveOptions{As: SaveAsFragment, Automation: "hackernews"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != base+"+local.1" || res.Action != "fill_contact_form" {
		t.Fatalf("result = %+v", res)
	}
	p, err := reg.Get("hackernews")
	if err != nil {
		t.Fatal(err)
	}
	if f, err := p.Fragment("fill_contact_form"); err != nil || len(f.Steps) != 5 {
		t.Errorf("fragment = %+v %v", f, err)
	}
	if info, _ := reg.Info("hackernews"); info.Source != automation.SourceBuiltin {
		t.Errorf("source = %q", info.Source)
	}
	if err := reg.Seed(hackernewsSeed(t, "9.9.9")); err != nil {
		t.Fatal(err)
	}
	if info, _ := reg.Info("hackernews"); info.PendingUpdate != "9.9.9" {
		t.Errorf("pending = %q", info.PendingUpdate)
	}
}
