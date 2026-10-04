package monomind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// fakeFailingModelsMonomind is a monomind with the agent-models capability whose
// listing fails for claude and dsh (it exits non-zero, with nothing to parse) and
// has no listing command for pi.
func fakeFailingModelsMonomind(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	bin := filepath.Join(t.TempDir(), "monomind")
	script := "#!/bin/sh\n" +
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"9.0.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1","agent-models"]}'; exit 0; fi` + "\n" +
		`if [ "$1 $2 $3 $4" = "agent models --runtime pi" ]; then echo '{"v":1,"runtime":"pi","supported":false,"models":[]}'; exit 0; fi` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOverride, bin)
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)
}

// ListModels answers a failed listing with the built-in list, so a picker is
// never empty. A caller that serves model ids to clients needs to know that the
// list is only standing in, and ListModelsStrict says so.
func TestListModelsStrictSaysWhenABuiltInListStandsInForAFailedListing(t *testing.T) {
	fakeFailingModelsMonomind(t)
	ctx := context.Background()

	for runtimeID, want := range map[string][]RuntimeModel{"claude": claudeModels, "dsh": curatedModels["dsh"]} {
		got, err := ListModelsStrict(ctx, runtimeID, "")
		if !errors.Is(err, ErrBuiltinModels) || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: strict = %d models, %v; want the built-in list and ErrBuiltinModels", runtimeID, len(got), err)
		}
		// Every other caller keeps what it always got.
		got, err = ListModels(ctx, runtimeID, "")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: ListModels = %d models, %v; want the built-in list and no error", runtimeID, len(got), err)
		}
	}

	// A runtime with no listing command is not a failure: its curated list is the answer.
	if got, err := ListModelsStrict(ctx, "pi", ""); err != nil || !reflect.DeepEqual(got, curatedModels["pi"]) {
		t.Errorf("pi (no listing command): %d models, %v; want the curated list and no error", len(got), err)
	}
}

// antigravity's and codex's built-in source is the runtime's own listing command,
// as live as monomind's: when monomind's listing fails and that one answers, the
// list is not a stand-in.
func TestListModelsStrictDoesNotFlagARuntimesOwnListingCommand(t *testing.T) {
	fakeFailingModelsMonomind(t)
	agy := writeFakeScript(t, "#!/bin/sh\necho 'gemini-3.8-flash-high\tGemini 3.8 Flash (High)'\n")
	got, err := ListModelsStrict(context.Background(), "antigravity", agy)
	if err != nil || len(got) != 1 || got[0].ID != "gemini-3.8-flash-high" {
		t.Errorf("antigravity's own listing is a live list: %+v, %v", got, err)
	}
}

func TestListModelsStrictDoesNotFlagALiveListOrAnOldMonomind(t *testing.T) {
	fakeAgentModelsMonomind(t, true)
	if _, err := ListModelsStrict(context.Background(), "claude", ""); err != nil {
		t.Errorf("a live listing is not a stand-in: %v", err)
	}
	fakeAgentModelsMonomind(t, false)
	if got, err := ListModelsStrict(context.Background(), "claude", ""); err != nil || !reflect.DeepEqual(got, claudeModels) {
		t.Errorf("a monomind without agent models has only the built-in list, which is the answer: %d models, %v", len(got), err)
	}
}
