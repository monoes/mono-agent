package openaiapi

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// deepTree makes a chain of directories deeper than emptyDir will walk, with a
// file at the bottom, under dir.
func deepTree(t *testing.T, dir string) string {
	t.Helper()
	deepest := dir
	for i := 0; i < maxCleanDepth+20; i++ {
		deepest = filepath.Join(deepest, "d")
	}
	if err := os.MkdirAll(deepest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deepest, "left-behind.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return deepest
}

// A folder that cannot be emptied is neither retried on every request nor does
// it refuse them: it is set aside for the operator, and the turn runs in an
// empty one. It never runs among what an earlier turn left.
func TestRunTurnSetsAsideAFolderItCannotEmptyAndRunsInAFreshOne(t *testing.T) {
	var cwd string
	var leftovers int
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		cwd = opts.Cwd
		entries, _ := os.ReadDir(opts.Cwd)
		leftovers = len(entries)
		return okTurn("ok")(ctx, opts, onEvent)
	})
	slot := filepath.Join(h.scratch, profileFolder(""), "slot-0")
	deepest := deepTree(t, slot)

	if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, Slot: 0}); err != nil {
		t.Fatalf("a folder that cannot be emptied must be set aside, not refuse the request: %v", err)
	}
	if cwd != slot || leftovers != 0 {
		t.Errorf("the turn must run in an empty folder at the same path: cwd=%q (want %q), %d entries", cwd, slot, leftovers)
	}
	aside, _ := os.ReadDir(filepath.Join(h.scratch, quarantineDirName))
	if len(aside) != 1 {
		t.Fatalf("the old folder must be set aside under %s: %d entries", quarantineDirName, len(aside))
	}
	rel, err := filepath.Rel(slot, deepest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.scratch, quarantineDirName, aside[0].Name(), rel)); err != nil {
		t.Errorf("what the turn left must be in the set-aside folder, untouched: %v", err)
	}
	if lines := strings.Join(h.logged(), "\n"); !strings.Contains(lines, quarantineDirName) {
		t.Errorf("the operator must be told where the folder went: %q", lines)
	}
}

// A folder that can be neither emptied nor set aside still refuses the turn.
func TestRunTurnRefusesWhenTheFolderCanBeNeitherEmptiedNorSetAside(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		ran.Store(true)
		return okTurn("ok")(ctx, opts, onEvent)
	})
	deepTree(t, filepath.Join(h.scratch, profileFolder(""), "slot-0"))
	if err := os.WriteFile(filepath.Join(h.scratch, quarantineDirName), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, Slot: 0}); err == nil {
		t.Fatal("a turn must not start in a folder that still holds what an earlier turn left")
	}
	if ran.Load() {
		t.Error("nothing must run when the folder is not clean")
	}
}

// A link planted in place of the profile's folder must not get a slot folder
// created in what it points to before the turn is refused.
func TestSlotDirCreatesNothingBehindAPlantedLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	h := newHarness(t, okTurn("ok"))
	outside := t.TempDir()
	if err := os.MkdirAll(h.scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(h.scratch, profileFolder(""))); err != nil {
		t.Fatal(err)
	}
	if _, err := h.g.slotDir("", 0); err == nil {
		t.Fatal("a symlinked profile folder must be refused")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the refusal came after a folder was created behind the link: %d entries", len(entries))
	}
}

// The folder of a turn's prompt files holds a context key's excerpts: whatever
// mode a leftover folder had, it is tightened to 0700.
func TestTurnTempDirTightensALooserFolder(t *testing.T) {
	h := newHarness(t, okTurn("ok"))
	loose := filepath.Join(h.scratch, tmpDirName)
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp, err := h.g.turnTempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if fi, err := os.Stat(loose); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the folder holding the turns' files must be 0700: %v %v", fi, err)
	}
}
