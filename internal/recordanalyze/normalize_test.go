package recordanalyze

import (
	"reflect"
	"testing"

	"github.com/monoes/mono-agent/internal/recording"
)

func TestNormalizeFormSubmit(t *testing.T) {
	a := analyzeFixture(t, "form-submit")
	want := []string{KindNavigate, KindType, KindType, KindType, KindClick}
	if got := kinds_(a.Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	email := a.Steps[1]
	if email.Value != "jane@example.com" {
		t.Errorf("merged value = %q", email.Value)
	}
	if !reflect.DeepEqual(email.Merged, []string{"e3", "e2", "e4"}) {
		t.Errorf("merged ids = %v", email.Merged)
	}
	if !a.Steps[2].Marked || a.Steps[2].Param != "full_name" {
		t.Errorf("param mark not applied: %+v", a.Steps[2])
	}
	if !a.Steps[3].Masked || a.Steps[3].Value != "" {
		t.Errorf("password step = %+v", a.Steps[3])
	}
	save := a.Steps[4]
	if !save.Submits || save.NavigatedTo != "https://app.acme-crm.test/contacts/4821" {
		t.Errorf("save = %+v", save)
	}
	if len(a.Segments) != 1 || !reflect.DeepEqual(a.Domains, []string{"app.acme-crm.test"}) {
		t.Errorf("segments %v domains %v", a.Segments, a.Domains)
	}
	if save.Candidates[0].CSS != `[data-testid="contact-save"]` {
		t.Errorf("best candidate = %+v", save.Candidates[0])
	}
}

func TestNormalizeScrollKeptOnlyBeforeExtract(t *testing.T) {
	a := analyzeFixture(t, "list-scrape")
	want := []string{KindNavigate, KindScroll, KindExtract, KindExtract}
	if got := kinds_(a.Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(a.Steps[1].Merged, []string{"e2", "e3"}) {
		t.Errorf("scrolls not collapsed: %v", a.Steps[1].Merged)
	}

	evs := []recording.Event{
		{ID: "e1", Seq: 1, Type: recording.EvNavigate, URL: "https://x.test/a"},
		{ID: "e2", Seq: 2, Type: recording.EvScroll, URL: "https://x.test/a"},
		{ID: "e3", Seq: 3, Type: recording.EvClick, URL: "https://x.test/a", Target: &recording.Fingerprint{Tag: "a", CSS: "a.b"}},
	}
	n := Normalize(&recording.Summary{URL: "https://x.test/a"}, evs)
	if got := kinds_(n.Steps); !reflect.DeepEqual(got, []string{KindNavigate, KindClick}) {
		t.Errorf("stray scroll kept: %v", got)
	}
}

func TestNormalizeDropsNoise(t *testing.T) {
	btn := &recording.Fingerprint{Tag: "button", CSS: "#go"}
	evs := []recording.Event{
		{ID: "e1", Seq: 1, Type: recording.EvNavigate, URL: "https://x.test/"},
		{ID: "e2", Seq: 2, Type: recording.EvNavigate, URL: "https://x.test/#top"},         // duplicate
		{ID: "e3", Seq: 3, Type: recording.EvPressKey, URL: "https://x.test/", Key: "Tab"}, // focus noise
		{ID: "e4", Seq: 4, T: 1000, Type: recording.EvClick, URL: "https://x.test/", Target: btn},
		{ID: "e5", Seq: 5, T: 1100, Type: recording.EvClick, URL: "https://x.test/", Target: btn}, // dblclick
		{ID: "e6", Seq: 6, Type: recording.EvPressKey, URL: "https://x.test/", Key: "Escape"},
		{ID: "e7", Seq: 7, Type: recording.EvNavigate, URL: "https://x.test/b"},
		{ID: "e8", Seq: 8, Type: recording.EvNavigate, URL: "https://x.test/c"}, // replaces e7
	}
	n := Normalize(nil, evs)
	if got := kinds_(n.Steps); !reflect.DeepEqual(got, []string{KindNavigate, KindClick, KindPressKey, KindNavigate}) {
		t.Fatalf("kinds = %v", got)
	}
	if n.Steps[3].URL != "https://x.test/c" {
		t.Errorf("later navigate should win: %q", n.Steps[3].URL)
	}
	if len(n.Segments) != 2 {
		t.Errorf("segments = %+v", n.Segments)
	}
}

func TestNormalizeAddsStartNavigate(t *testing.T) {
	evs := []recording.Event{{ID: "e1", Seq: 1, Type: recording.EvClick, URL: "https://x.test/p", Target: &recording.Fingerprint{Tag: "a", ID: "go"}}}
	n := Normalize(nil, evs)
	if len(n.Steps) != 2 || n.Steps[0].Kind != KindNavigate || n.Steps[0].URL != "https://x.test/p" {
		t.Fatalf("steps = %+v", n.Steps)
	}
	if n.Steps[1].Candidates[0].CSS != "#go" {
		t.Errorf("fallback candidates = %+v", n.Steps[1].Candidates)
	}
}

func TestRankCandidatesScoresDownNonUnique(t *testing.T) {
	fp := &recording.Fingerprint{Candidates: []recording.Candidate{
		{Kind: "css", Value: ".btn", Unique: false, Count: 3, Score: 0.9},
		{Kind: "aria", Role: "button", Name: "Go", Unique: true, Count: 1, Score: 0.7},
	}}
	got := rankCandidates(fp)
	if got[0].Aria == nil || got[1].CSS != ".btn" || got[1].Score != 0.45 {
		t.Errorf("ranked = %+v", got)
	}
}

func TestURLPattern(t *testing.T) {
	cases := map[string]string{
		"https://a.test/orders/10023":                             "a.test/orders/:id",
		"https://a.test/u/3f2b1c4a-1111-2222-3333-444455556666/x": "a.test/u/:id/x",
		"https://a.test/contacts/new":                             "a.test/contacts/new",
		"https://a.test/p/abc123XYZ9":                             "a.test/p/:id",
		"https://a.test/blog/my-first-post":                       "a.test/blog/my-first-post",
	}
	for in, want := range cases {
		if got := urlPattern(in); got != want {
			t.Errorf("urlPattern(%s) = %s, want %s", in, got, want)
		}
	}
}
