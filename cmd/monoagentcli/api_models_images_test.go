package main

import (
	"slices"
	"strings"
	"testing"
)

func capabilitiesByID(m apiModelsJSON) map[string][]string {
	out := map[string][]string{}
	for _, x := range m.Models {
		out[x.ID] = x.Capabilities
	}
	return out
}

// `api models` says which models make images, so that an operator and a script can
// see what GET /v1/models would say before a server is started.
func TestAPIModelsShowsWhichModelsMakeImages(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", "")
	text, both := []string{"text"}, []string{"text", "image"}

	out, _, err := runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	got := capabilitiesByID(decodeModels(t, out))
	for id, want := range map[string][]string{
		"claude/default": text, "codex/default": both, "codex/gpt-6-astra": both,
		"antigravity/default": both, "antigravity/gemini-3.8-flash-high": both,
	} {
		if !slices.Equal(got[id], want) {
			t.Errorf("%s: capabilities %v, want %v", id, got[id], want)
		}
	}

	// The list is the shell's environment, like every other setting api models evaluates.
	t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", "agy")
	out, _, err = runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	got = capabilitiesByID(decodeModels(t, out))
	if !slices.Equal(got["antigravity/default"], both) || !slices.Equal(got["codex/default"], text) || !slices.Equal(got["claude/default"], text) {
		t.Errorf("MONOAGENT_API_IMAGE_RUNTIMES=agy: %v", got)
	}

	// none switches image generation off: no model makes images.
	t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", "none")
	out, _, err = runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatalf("MONOAGENT_API_IMAGE_RUNTIMES=none: %v", err)
	}
	for id, caps := range capabilitiesByID(decodeModels(t, out)) {
		if !slices.Equal(caps, text) {
			t.Errorf("MONOAGENT_API_IMAGE_RUNTIMES=none: %s has capabilities %v, want text alone", id, caps)
		}
	}

	// The existing fields are where they were.
	if !strings.Contains(out, `"auto_allowed":`) || !strings.Contains(out, `"context_allowed":`) {
		t.Errorf("the existing fields are gone:\n%s", out)
	}
}

func TestAPIModelsRefusesABadImageRuntimeList(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", "co dex")
	_, _, err := runAPI(t, db, "default", true, "models")
	if exitCodeFor(err) != 3 || err == nil || !strings.Contains(err.Error(), "MONOAGENT_API_IMAGE_RUNTIMES") {
		t.Fatalf("a bad list is invalid input (exit 3) that names the variable: %v", err)
	}
}

// The table has a column for it, after the ones that were there.
func TestAPIModelsTableHasAnImagesColumn(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", "")

	out, _, err := runAPI(t, db, "default", false, "models")
	if err != nil {
		t.Fatal(err)
	}
	var header string
	rows := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		switch {
		case strings.HasPrefix(line, "MODEL"):
			header = line
		case len(f) > 0 && (strings.HasPrefix(f[0], "claude/") || strings.HasPrefix(f[0], "codex/") || strings.HasPrefix(f[0], "antigravity/")):
			rows[f[0]] = f
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(header), "AUTO  IMAGES") {
		t.Fatalf("the IMAGES column goes last: %q", header)
	}
	for id, want := range map[string]string{"claude/default": "no", "codex/gpt-6-astra": "yes", "antigravity/default": "yes"} {
		if f := rows[id]; len(f) == 0 || f[len(f)-1] != want {
			t.Errorf("%s: %v, want the IMAGES column to say %s", id, f, want)
		}
	}
}
