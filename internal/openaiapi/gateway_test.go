package openaiapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewRequiresItsDependencies(t *testing.T) {
	good := func(t *testing.T) (Deps, Config) {
		h := newHarness(t, okTurn("x"))
		return h.g.deps, Config{ScratchRoot: t.TempDir()}
	}
	for name, drop := range map[string]func(*Deps){
		"keys":           func(d *Deps) { d.Keys = nil },
		"exec":           func(d *Deps) { d.Exec = nil },
		"bin":            func(d *Deps) { d.Bin = nil },
		"catalog scan":   func(d *Deps) { d.Catalog.Scan = nil },
		"catalog models": func(d *Deps) { d.Catalog.Models = nil },
	} {
		d, c := good(t)
		drop(&d)
		if _, err := New(d, c); err == nil {
			t.Errorf("New accepted a gateway without %s", name)
		}
	}
	d, c := good(t)
	if _, err := New(d, c); err != nil {
		t.Fatalf("New with every dependency: %v", err)
	}
}

// A profile's folders are named by a hash of its id: the id is an arbitrary
// string and must never steer a path.
func TestProfileFolderIsStableDistinctAndSafe(t *testing.T) {
	if profileFolder("alice") != profileFolder("alice") || profileFolder("alice") == profileFolder("bob") {
		t.Errorf("a profile must always get the same folder and two profiles different ones: %q %q %q",
			profileFolder("alice"), profileFolder("alice"), profileFolder("bob"))
	}
	shape := regexp.MustCompile(`^p-[0-9a-f]{16}$`)
	for _, id := range []string{"alice", "", "..", ".", "../../etc", "a/b", `a\b`, "with space", "ünï", strings.Repeat("x", 500)} {
		if f := profileFolder(id); !shape.MatchString(f) {
			t.Errorf("profileFolder(%q) = %q: a profile id must never reach a path as such", id, f)
		}
	}
}

func TestNewEmptiesLeftoverSlotFoldersAndNothingElse(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, profileFolder("alice"))
	inSlot, aboveLimit := filepath.Join(profile, "slot-1"), filepath.Join(profile, "slot-9")
	notASlot, notAProfile := filepath.Join(profile, "keep-me"), filepath.Join(root, "keep-me")
	foreignSlot := filepath.Join(notAProfile, "slot-1") // a slot-looking folder in a folder that is not a profile's
	for _, d := range []string{inSlot, aboveLimit, notASlot, notAProfile, foreignSlot} {
		if err := os.MkdirAll(filepath.Join(d, "sub"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "left-by-a-crash.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_ = newHarness(t, okTurn("x"), func(_ *Deps, c *Config) { c.ScratchRoot, c.MaxConcurrent = root, 4 })
	if entries, err := os.ReadDir(inSlot); err != nil || len(entries) != 0 {
		t.Errorf("a slot folder must be emptied and kept: %d entries, %v", len(entries), err)
	}
	if _, err := os.Stat(aboveLimit); !os.IsNotExist(err) {
		t.Error("the folder of a slot above the limit must be removed")
	}
	for _, untouched := range []string{notASlot, foreignSlot} {
		if entries, _ := os.ReadDir(untouched); len(entries) != 2 {
			t.Errorf("only slot-* folders inside a profile folder may be touched: %s", untouched)
		}
	}
	if entries, _ := os.ReadDir(notAProfile); len(entries) != 3 { // sub, the file, slot-1
		t.Errorf("a folder that is not a profile's must be left alone: %d entries", len(entries))
	}
}

func TestDrainWaitsUntilEveryStartedTurnHasEnded(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	if !h.g.Drain(time.Second) {
		t.Fatal("with nothing running, Drain returns at once")
	}
	h.g.turnStarted()
	if h.g.Drain(50 * time.Millisecond) {
		t.Fatal("Drain must not report done while a turn runs")
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.g.turnEnded()
	}()
	if !h.g.Drain(5 * time.Second) {
		t.Fatal("Drain must return once the turn has ended")
	}
}

func TestBinIsCachedButErrorsAreNot(t *testing.T) {
	var calls atomic.Int32
	fail := true
	h := newHarness(t, okTurn("x"), func(d *Deps, _ *Config) {
		d.Bin = func(context.Context) (string, error) {
			calls.Add(1)
			if fail {
				return "", errors.New("monomind not found")
			}
			return "/fake/monomind", nil
		}
	})
	ctx := context.Background()
	if _, err := h.g.bin.get(ctx); err == nil {
		t.Fatal("expected the lookup error")
	}
	fail = false
	for range 3 {
		if got, err := h.g.bin.get(ctx); err != nil || got != "/fake/monomind" {
			t.Fatalf("bin = %q, %v", got, err)
		}
	}
	if got := calls.Load(); got != 2 { // the failure, then one successful lookup
		t.Errorf("Bin was called %d times, want 2: errors must not be cached, successes must", got)
	}
}

func TestConfigDefaultsAndEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := Config{}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrent != 4 || cfg.TurnTimeout != 10*time.Minute || cfg.BodyLimit != 2<<20 || cfg.CatalogTTL != 5*time.Minute ||
		cfg.StreamCommitAfter != 5*time.Second || cfg.KeepAlive != 15*time.Second {
		t.Errorf("defaults: %+v", cfg)
	}
	if filepath.Base(cfg.ScratchRoot) != "api" || filepath.Base(filepath.Dir(cfg.ScratchRoot)) != "workspaces" {
		t.Errorf("ScratchRoot = %q, want ~/.monoagent/workspaces/api", cfg.ScratchRoot)
	}

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	got, err := ConfigFromEnv(env(map[string]string{"MONOAGENT_API_MAX_CONCURRENT": "8", "MONOAGENT_API_TURN_TIMEOUT": "15m"}))
	if err != nil || got.MaxConcurrent != 8 || got.TurnTimeout != 15*time.Minute {
		t.Errorf("ConfigFromEnv = %+v, %v", got, err)
	}
	if got, err := ConfigFromEnv(env(nil)); err != nil || got.MaxConcurrent != 0 || got.TurnTimeout != 0 {
		t.Errorf("unset variables must leave the defaults to withDefaults: %+v, %v", got, err)
	}
	for k, v := range map[string]string{
		"MONOAGENT_API_MAX_CONCURRENT": "0",
		"MONOAGENT_API_TURN_TIMEOUT":   "5s",
	} {
		if _, err := ConfigFromEnv(env(map[string]string{k: v})); err == nil {
			t.Errorf("%s=%s was accepted", k, v)
		}
	}
	for _, v := range []string{"many", "-2", "1.5"} {
		if _, err := ConfigFromEnv(env(map[string]string{"MONOAGENT_API_MAX_CONCURRENT": v})); err == nil {
			t.Errorf("MONOAGENT_API_MAX_CONCURRENT=%s was accepted", v)
		}
	}
	if _, err := ConfigFromEnv(env(map[string]string{"MONOAGENT_API_TURN_TIMEOUT": "soon"})); err == nil {
		t.Error("MONOAGENT_API_TURN_TIMEOUT=soon was accepted")
	}
}
