package openaiapi

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// The models auto may pick for an image request are the models that can make images
// within what auto may pick: held to chat-only until the operator raises
// --auto-confinement, which none of them is, so by default there are none, and the
// error says what to raise.
func TestAutoCandidatesForImagesAreTheImageModelsAutoMayPick(t *testing.T) {
	ctxKey, plain := Principal{ProfileID: "alice", Context: true}, Principal{ProfileID: "alice"}
	for _, c := range []struct {
		name string
		cfg  []string // the image list; nil is the default
		p    Policy
		pr   Principal
		want []string // the candidates, sorted; nil when there are none
		hint []string // what the 404 says to do when there are none
	}{
		{"nothing granted to auto", nil, anyPolicy, plain, nil, []string{"--auto-confinement", "sandboxed"}},
		{"sandboxed granted", nil, Policy{Max: Unconfined, AutoMax: Sandboxed}, plain, []string{"codex/default", "codex/gpt-6-astra"}, nil},
		{"everything granted", nil, autoAnyPolicy, plain, []string{"antigravity/default", "antigravity/gemini-3.8-flash-high", "codex/default", "codex/gpt-6-astra"}, nil},
		{"granted above a sandboxed server", nil, Policy{Max: Sandboxed, AutoMax: Unconfined}, plain, []string{"codex/default", "codex/gpt-6-astra"}, nil},
		{"granted above a chat-only server", nil, Policy{Max: ChatOnly, AutoMax: Unconfined}, plain, nil, []string{"--confinement"}},
		{"a context key held to chat-only", nil, Policy{Max: Unconfined, AutoMax: Unconfined}, ctxKey, nil, []string{"--context-confinement"}},
		{"a context key raised to sandboxed", nil, Policy{Max: Unconfined, ContextMax: Sandboxed, AutoMax: Unconfined}, ctxKey, []string{"codex/default", "codex/gpt-6-astra"}, nil},
		{"a configured list", []string{"hermes"}, autoAnyPolicy, plain, []string{"hermes/default"}, nil},
		{"a list of runtimes that are not installed", []string{"grok"}, autoAnyPolicy, plain, nil, []string{"grok"}},
	} {
		h := autoGateway(t, &fakeAuto{id: "claude/default", p: 1}, func(_ *Deps, cfg *Config) { cfg.ImageRuntimes = c.cfg })
		eff := policyFor(c.p, c.pr)

		got, e := h.g.autoCandidatesFor(context.Background(), c.pr, eff, capImage)
		if c.want == nil {
			if e == nil || e.Status != http.StatusNotFound || e.Code != "model_not_found" || e.Param != "model" {
				t.Errorf("%s: %v %+v, want a 404 model_not_found", c.name, ids(got), e)
				continue
			}
			for _, want := range c.hint {
				if !strings.Contains(e.Message, want) {
					t.Errorf("%s: %q must say %q", c.name, e.Message, want)
				}
			}
			continue
		}
		if e != nil {
			t.Errorf("%s: %+v", c.name, e)
			continue
		}
		if g := slices.Sorted(slices.Values(ids(got))); !slices.Equal(g, c.want) {
			t.Errorf("%s: candidates %v, want %v", c.name, g, c.want)
		}
	}
}

// Chat's candidates are what they were: every model auto may pick.
func TestAutoCandidatesForChatAreUnchanged(t *testing.T) {
	h := autoGateway(t, &fakeAuto{id: "claude/default", p: 1})
	got, e := h.g.autoCandidates(context.Background(), Principal{ProfileID: "alice"}, anyPolicy)
	if e != nil || !slices.Equal(slices.Sorted(slices.Values(ids(got))), []string{"claude/default", "claude/opus[1m]"}) {
		t.Fatalf("%v %+v", ids(got), e)
	}
	got, e = h.g.autoCandidates(context.Background(), Principal{ProfileID: "alice"}, autoAnyPolicy)
	if e != nil || len(got) != 7 {
		t.Fatalf("with everything granted: %d candidates, %+v", len(got), e)
	}
}

// A server with no chat-only model installed has nothing for auto to pick among
// until the operator raises --auto-confinement: the 404 says so, in the words it
// has always used for chat.
func TestAutoCandidatesForChatSayWhatIsMissingWhenThereAreNone(t *testing.T) {
	h := autoGateway(t, &fakeAuto{id: "codex/default", p: 1}, func(d *Deps, _ *Config) {
		scan := d.Catalog.Scan
		d.Catalog.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
			res, err := scan(ctx)
			if err != nil {
				return nil, err
			}
			only := *res
			only.Agents = nil
			for _, a := range res.Agents {
				if a.ID == "codex" || a.ID == "antigravity" { // no claude: nothing is chat-only
					only.Agents = append(only.Agents, a)
				}
			}
			return &only, nil
		}
	})
	_, e := h.g.autoCandidates(context.Background(), Principal{ProfileID: "alice"}, anyPolicy)
	if e == nil || e.Status != http.StatusNotFound || e.Code != "model_not_found" || !strings.Contains(e.Message, "at least one model the server's confinement policy allows auto to pick") {
		t.Fatalf("%+v", e)
	}
	if got, e := h.g.autoCandidates(context.Background(), Principal{ProfileID: "alice"}, autoAnyPolicy); e != nil || len(got) != 4 {
		t.Fatalf("with everything granted: %v %+v", ids(got), e)
	}
}

// The auto model says it can make images only when an image request would find a
// candidate, so a client that reads the capabilities is not promised what would
// answer 404.
func TestAutoModelReportsTheImageCapabilityOnlyWithImageCandidates(t *testing.T) {
	h := autoGateway(t, &fakeAuto{id: "claude/default", p: 1})
	secret := h.key(t, "default", "app", false)
	for _, c := range []struct {
		p    Policy
		want []string
	}{
		{anyPolicy, []string{"text"}}, // nothing granted to auto
		{Policy{Max: Unconfined, AutoMax: Sandboxed}, []string{"text", "image"}},
		{autoAnyPolicy, []string{"text", "image"}},
		{Policy{Max: ChatOnly, AutoMax: Unconfined}, []string{"text"}}, // the server serves no image model
	} {
		list := decodeModelList(t, h.serve(c.p, http.MethodGet, "/v1/models", secret, ""))
		a := list.Data[len(list.Data)-1]
		if a.ID != "auto" || !slices.Equal(noTools(a.Monoagent.Capabilities), c.want) {
			t.Errorf("%+v: the list has auto with %v, want %v", c.p, a.Monoagent.Capabilities, c.want)
		}
		var one modelObject
		decodeInto(t, h.serve(c.p, http.MethodGet, "/v1/models/auto", secret, ""), &one)
		if !slices.Equal(noTools(one.Monoagent.Capabilities), c.want) {
			t.Errorf("%+v: GET /v1/models/auto has %v, want %v", c.p, one.Monoagent.Capabilities, c.want)
		}
	}
}
