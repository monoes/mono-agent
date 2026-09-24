package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsMonomindInitializedAt(t *testing.T) {
	root := t.TempDir()

	if isMonomindInitializedAt(root) {
		t.Fatal("expected false for a profile folder with no .monomind/config.yaml")
	}

	monomindDir := filepath.Join(root, ".monomind")
	if err := os.MkdirAll(monomindDir, 0700); err != nil {
		t.Fatal(err)
	}
	if isMonomindInitializedAt(root) {
		t.Fatal("expected false: .monomind/ exists but config.yaml does not (EnsureLayout creates the bare dir on every org call)")
	}

	if err := os.WriteFile(filepath.Join(monomindDir, "config.yaml"), []byte("name: test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !isMonomindInitializedAt(root) {
		t.Fatal("expected true once .monomind/config.yaml exists")
	}
}

// The fix's NDJSON becomes the init events the frontend has always had.
func TestRelayFixEvents(t *testing.T) {
	in := strings.Join([]string{
		`{"kind":"line","message":"copying skills"}`,
		`not json`,
		`{"kind":"done","message":"Set up this profile's folder"}`,
	}, "\n")
	var got []string
	if !relayFixEvents(strings.NewReader(in), func(kind, msg string) { got = append(got, kind+":"+msg) }) {
		t.Fatal("done not seen")
	}
	want := []string{"line:copying skills", "line:not json", "done:"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events %v, want %v", got, want)
	}
	got = nil
	if !relayFixEvents(strings.NewReader(`{"kind":"error","message":"exit status 127"}`), func(kind, msg string) { got = append(got, kind+":"+msg) }) ||
		got[0] != "error:exit status 127" {
		t.Fatalf("error event: %v", got)
	}
	if relayFixEvents(strings.NewReader(`{"kind":"line","message":"x"}`), func(string, string) {}) {
		t.Fatal("a stream without done/error must not count as finished")
	}
}

// The binding shells out to the CLI's fix, with the active profile.
func TestMonomindInitRunsTheDoctorFix(t *testing.T) {
	got := strings.Join(healthFixArgs("p1", fixMonomindProfileInit), " ")
	if got != "--profile p1 --json doctor fix -- monomind.profile_init" {
		t.Fatalf("args %q", got)
	}
}
