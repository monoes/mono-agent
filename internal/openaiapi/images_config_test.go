package openaiapi

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

func imageEnv(v string) func(string) string {
	return func(k string) string {
		if k == "MONOAGENT_API_IMAGE_RUNTIMES" {
			return v
		}
		return ""
	}
}

// Which runtimes make images is a list the operator can change, not knowledge of
// any CLI: it is read once, validated, and the order is the order of "the first
// installed one".
func TestParseImageRuntimes(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", []string{"codex", "antigravity"}},
		{"   ", []string{"codex", "antigravity"}},
		{"codex", []string{"codex"}},
		{"antigravity,codex", []string{"antigravity", "codex"}},
		{" Codex , AGY ,codex", []string{"codex", "antigravity"}}, // case, spaces, the agy alias and a repeat
		{"hermes", []string{"hermes"}},
	} {
		got, err := ParseImageRuntimes(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("ParseImageRuntimes(%q) = %v, %v, want %v", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{",", "codex,", ",codex", "codex,,agy", "--x", "a b", "co/dex", "codex;ls", strings.Repeat("a", 33), "none,codex", "codex,none"} {
		if _, err := ParseImageRuntimes(in); err == nil || !strings.Contains(err.Error(), "MONOAGENT_API_IMAGE_RUNTIMES") {
			t.Errorf("ParseImageRuntimes(%q) = %v, want an error that names the variable", in, err)
		}
	}
}

// none is the off switch: a list with nothing in it, which is not the default one, however
// it is written, and it is not a runtime to list with others.
func TestParseImageRuntimesNoneSwitchesImagesOff(t *testing.T) {
	for _, in := range []string{"none", " None ", "NONE"} {
		got, err := ParseImageRuntimes(in)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("ParseImageRuntimes(%q) = %#v, %v, want a list that is empty and not nil", in, got, err)
		}
	}
	off, err := ConfigFromEnv(imageEnv("none"))
	if err != nil || off.ImageRuntimes == nil || !off.ImagesOff() || len(off.ImageRuntimeList()) != 0 {
		t.Fatalf("ConfigFromEnv(none) = %#v, %v: image generation is off, and the list is not the default", off.ImageRuntimes, err)
	}
	if off.CanMakeImages(ModelInfo{Runtime: "codex", Class: Sandboxed}) || off.CanMakeImages(ModelInfo{Runtime: "antigravity", Class: Unconfined}) {
		t.Error("a model can make images with image generation switched off")
	}
	if got := off.Capabilities(ModelInfo{Runtime: "codex", Class: Sandboxed}); !slices.Equal(got, []string{"text"}) {
		t.Errorf("capabilities with image generation off: %v", got)
	}
	if (Config{}).ImagesOff() || (Config{ImageRuntimes: []string{"codex"}}).ImagesOff() {
		t.Error("image generation is on unless the list was set to nothing")
	}
}

func TestConfigFromEnvReadsTheImageRuntimes(t *testing.T) {
	got, err := ConfigFromEnv(imageEnv("agy, codex"))
	if err != nil || !slices.Equal(got.ImageRuntimes, []string{"antigravity", "codex"}) {
		t.Errorf("ConfigFromEnv = %v, %v", got.ImageRuntimes, err)
	}
	// Unset leaves the default to the methods, like every other setting.
	if got, err := ConfigFromEnv(imageEnv("")); err != nil || got.ImageRuntimes != nil {
		t.Errorf("an unset variable must leave the list empty: %v, %v", got.ImageRuntimes, err)
	}
	if _, err := ConfigFromEnv(imageEnv("co dex")); err == nil {
		t.Error("MONOAGENT_API_IMAGE_RUNTIMES=\"co dex\" was accepted")
	}
	if got := (Config{}).ImageRuntimeList(); !slices.Equal(got, []string{"codex", "antigravity"}) {
		t.Errorf("the default list = %v, want codex then antigravity", got)
	}
}

// An image model is a listed runtime's model that can write a file: a chat-only
// runtime has no native tool to save one, however the operator lists it.
func TestCanMakeImages(t *testing.T) {
	def := Config{}
	custom := Config{ImageRuntimes: []string{"hermes", "claude"}}
	for _, c := range []struct {
		cfg  Config
		m    ModelInfo
		want bool
	}{
		{def, ModelInfo{Runtime: "codex", Class: Sandboxed}, true},
		{def, ModelInfo{Runtime: "antigravity", Class: Unconfined}, true},
		{def, ModelInfo{Runtime: "claude", Class: ChatOnly}, false},   // not listed
		{def, ModelInfo{Runtime: "hermes", Class: Unconfined}, false}, // not listed
		{def, ModelInfo{Runtime: "codex", Class: ChatOnly}, false},    // listed, but cannot write a file
		{def, ModelInfo{Runtime: "codex"}, false},                     // a class nobody set
		{custom, ModelInfo{Runtime: "hermes", Class: Unconfined}, true},
		{custom, ModelInfo{Runtime: "claude", Class: ChatOnly}, false}, // listed, chat-only
		{custom, ModelInfo{Runtime: "codex", Class: Sandboxed}, false}, // the default list is replaced, not extended
	} {
		if got := c.cfg.CanMakeImages(c.m); got != c.want {
			t.Errorf("%v: CanMakeImages(%s %v) = %v, want %v", c.cfg.ImageRuntimes, c.m.Runtime, c.m.Class, got, c.want)
		}
	}
	if got := def.Capabilities(ModelInfo{Runtime: "codex", Class: Sandboxed}); !slices.Equal(got, []string{"text", "image"}) {
		t.Errorf("an image model: %v", got)
	}
	if got := def.Capabilities(ModelInfo{Runtime: "claude", Class: ChatOnly}); !slices.Equal(got, []string{"text"}) {
		t.Errorf("a text model: %v", got)
	}
}

// GET /v1/models says which models make images, so a client can choose without
// trying: the runtimes of the image list, as far as they can write a file.
func TestModelsAdvertiseTheImageCapability(t *testing.T) {
	text, both := []string{"text"}, []string{"text", "image"}
	for name, c := range map[string]struct {
		list []string // nil: the default
		want map[string][]string
	}{
		"the default list": {nil, map[string][]string{
			"claude/default": text, "claude/opus[1m]": text,
			"codex/default": both, "codex/gpt-6-astra": both,
			"antigravity/default": both, "antigravity/gemini-3.8-flash-high": both,
			"hermes/default": text,
		}},
		"a configured list": {[]string{"hermes", "claude"}, map[string][]string{
			"claude/default": text, "claude/opus[1m]": text, // listed, but chat-only
			"codex/default": text, "codex/gpt-6-astra": text,
			"antigravity/default": text, "antigravity/gemini-3.8-flash-high": text,
			"hermes/default": both,
		}},
		"switched off": {[]string{}, map[string][]string{
			"claude/default": text, "codex/default": text, "codex/gpt-6-astra": text,
			"antigravity/default": text, "hermes/default": text,
		}},
	} {
		h := newHarness(t, okTurn("x"), func(_ *Deps, cfg *Config) { cfg.ImageRuntimes = c.list })
		secret := h.key(t, "default", "app", false)

		got := map[string][]string{}
		for _, m := range decodeModelList(t, h.serve(anyPolicy, http.MethodGet, "/v1/models", secret, "")).Data {
			got[m.ID] = m.Monoagent.Capabilities
		}
		for id, want := range c.want {
			if !slices.Equal(got[id], want) {
				t.Errorf("%s: GET /v1/models: %s has capabilities %v, want %v", name, id, got[id], want)
			}
		}
		for _, id := range []string{"codex/gpt-6-astra", "claude/default", "hermes/default"} {
			var m modelObject
			decodeInto(t, h.serve(anyPolicy, http.MethodGet, "/v1/models/"+id, secret, ""), &m)
			if !slices.Equal(m.Monoagent.Capabilities, c.want[id]) {
				t.Errorf("%s: GET /v1/models/%s has capabilities %v, want %v", name, id, m.Monoagent.Capabilities, c.want[id])
			}
		}
	}
}
