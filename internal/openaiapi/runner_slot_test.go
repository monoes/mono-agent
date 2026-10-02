package openaiapi

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
		leftovers = len(besidesTmp(opts.Cwd))
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

// A folder a process that outlived its turn keeps changing may never be finished
// with, so emptying it is bounded in time, and one that takes too long is set aside
// as one that cannot be emptied, instead of holding the request, and its slot of
// the limiter, until it ends.
func TestRunTurnSetsAsideAFolderThatTakesTooLongToEmpty(t *testing.T) {
	withBudget(t, 200*time.Millisecond)
	var cwd string
	var leftovers int
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		cwd, leftovers = opts.Cwd, len(besidesTmp(opts.Cwd))
		return okTurn("ok")(ctx, opts, onEvent)
	})
	slot := filepath.Join(h.scratch, profileFolder(""), "slot-0")
	if err := os.MkdirAll(slot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slot, "old.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var calls atomic.Int32
	afterLstatHook.set(func() { // the first emptying, before the turn, never finishes
		if calls.Add(1) == 1 {
			<-release
		}
	})
	t.Cleanup(func() { afterLstatHook.set(nil) })
	t.Cleanup(func() { close(release) })

	errc := make(chan error, 1)
	go func() {
		_, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, Slot: 0})
		errc <- err
	}()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("a folder that takes too long to empty must be set aside, not refuse the request: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request waited for a folder it should have given up on")
	}
	if cwd != slot || leftovers != 0 {
		t.Errorf("the turn must run in an empty folder at the same path: cwd=%q (want %q), %d entries", cwd, slot, leftovers)
	}
	aside, _ := os.ReadDir(filepath.Join(h.scratch, quarantineDirName))
	if len(aside) != 1 {
		t.Fatalf("the old folder must be set aside under %s: %d entries", quarantineDirName, len(aside))
	}
	if _, err := os.Stat(filepath.Join(h.scratch, quarantineDirName, aside[0].Name(), "old.txt")); err != nil {
		t.Errorf("what was in the folder must be in the set-aside one: %v", err)
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

// A turn that replaced its own folder with a link (the sandbox of macOS lets it)
// must not have the cleanup follow it, and the next turn of that slot starts in
// a fresh folder: the link is set aside, so a slot can never be wedged by one.
func TestRunTurnDoesNotFollowAFolderTheTurnReplacedWithALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	outside := t.TempDir()
	if err := os.Chmod(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(precious, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	var turns int
	var cwds []string
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		turns++
		cwds = append(cwds, opts.Cwd)
		if turns == 1 { // what a turn confined only by "write below your folder" can do to that folder
			_ = os.RemoveAll(opts.Cwd)
			if err := os.Symlink(outside, opts.Cwd); err != nil {
				t.Error(err)
			}
		}
		return okTurn("ok")(ctx, opts, onEvent)
	})
	run := func() {
		t.Helper()
		if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, Slot: 0}); err != nil {
			t.Fatal(err)
		}
	}
	intact := func(when string) {
		t.Helper()
		if fi, err := os.Stat(outside); err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: the cleanup followed the link and changed what is behind it: %v %v", when, fi, err)
		}
		if _, err := os.Stat(precious); err != nil {
			t.Errorf("%s: the cleanup followed the link and removed what is behind it: %v", when, err)
		}
	}

	run() // the turn replaces its folder with a link; the cleanup after it must not follow the link
	intact("after the turn")

	run() // the next turn of the slot
	intact("after the next turn")
	slot := cwds[1]
	if fi, err := os.Lstat(slot); err != nil || !fi.IsDir() {
		t.Errorf("the next turn must run in a real folder at the same path: %v %v", fi, err)
	}
	if aside, _ := os.ReadDir(filepath.Join(h.scratch, quarantineDirName)); len(aside) != 1 {
		t.Errorf("the link must be set aside, not left to wedge the slot: %d entries", len(aside))
	}
}

// Two requests of a profile that has never run one can reach its folder at the
// same moment: neither may fail because the other created it first.
func TestSlotDirToleratesAConcurrentCreation(t *testing.T) {
	h := newHarness(t, okTurn("ok"))
	created := false
	beforeMkdirHook = func() {
		if created {
			return
		}
		created = true
		_ = os.MkdirAll(filepath.Join(h.scratch, profileFolder("racer")), 0o700) // the other request wins
	}
	t.Cleanup(func() { beforeMkdirHook = nil })

	if _, err := h.g.slotDir("racer", 0); err != nil {
		t.Fatalf("a folder another request created a moment earlier is fine: %v", err)
	}
	if !created {
		t.Fatal("the hook never ran: the test does not exercise the race")
	}
}

func TestSlotDirIsSafeForConcurrentFirstRequests(t *testing.T) {
	h := newHarness(t, okTurn("ok"))
	errs := make(chan error, 8)
	for slot := 0; slot < 8; slot++ {
		go func() {
			_, err := h.g.slotDir("fresh", slot)
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Errorf("a first request of a profile failed: %v", err)
		}
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

// monomind writes its own copy of a turn's prompt, system prompt or agent file
// under its temp directory (hermes, cline and kimicode do), and a sandboxed
// runtime may write the system's. A turn gets a temp folder of its own, inside
// its own folder: no other turn's sandbox reaches it, and the folder is emptied
// with the rest of the turn's files.
func TestRunTurnPointsTheRuntimesTempAtItsOwnFolder(t *testing.T) {
	var env map[string]string
	var execTmp string
	var existed bool
	var mode os.FileMode
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		env, execTmp = opts.Env, opts.TempDir
		info, err := os.Stat(opts.Env["TMPDIR"])
		existed = err == nil && info.IsDir()
		if err == nil {
			mode = info.Mode().Perm()
		}
		return okTurn("ok")(ctx, opts, onEvent)
	})
	if _, err := h.g.runTurn(context.Background(), turn{Runtime: "hermes", Model: "default", Prompt: "p", Policy: anyPolicy, ProfileID: "alice"}); err != nil {
		t.Fatal(err)
	}
	slot := filepath.Join(h.scratch, profileFolder("alice"), "slot-0")
	want := filepath.Join(slot, ".tmp")
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		if env[k] != want {
			t.Errorf("%s = %q, want the turn's own temp folder %q", k, env[k], want)
		}
	}
	if !existed || mode != 0o700 {
		t.Errorf("the temp folder must exist during the turn with mode 0700: existed=%v mode=%v", existed, mode)
	}
	// What Exec writes for monomind stays where no turn can write.
	if execTmp == "" || strings.HasPrefix(execTmp, slot) {
		t.Errorf("Exec's own files (%q) must stay outside the turn's folder %s", execTmp, slot)
	}
	if entries, _ := os.ReadDir(slot); len(entries) != 0 {
		t.Errorf("the turn's folder, its temp folder included, must be empty afterwards: %d entries", len(entries))
	}
}
