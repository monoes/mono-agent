package captureclassify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
)

func newClient(t *testing.T) *jev.Client {
	t.Helper()
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClassifyRequestShape(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"kind": "article"}))
	c := newClient(t)
	long := strings.Repeat("é", MaxContentChars+500)
	r, err := Classify(context.Background(), c, Input{URL: "https://x.test/a", Title: "A", Content: long})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "article" || r.P < 0.9 || r.Model != "jev-test" || r.At == "" || len(r.Probabilities) != len(Kinds) {
		t.Fatalf("unexpected result %+v", r)
	}
	if r.SuggestedRoute != "" {
		t.Fatalf("article must not suggest a route, got %q", r.SuggestedRoute)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || len(reqs[0].Questions) != 1 {
		t.Fatalf("want one request with one question, got %+v", reqs)
	}
	q := reqs[0].Questions["kind"]
	if q.Type != jev.TypeChoice || len(jev.OptionIDs(q)) != len(Kinds) {
		t.Fatalf("kind question = %+v", q)
	}
	if !strings.Contains(q.Instructions.(string), "untrusted_content") {
		t.Fatalf("instructions must fence untrusted_content: %v", q.Instructions)
	}
	state := reqs[0].State.(map[string]any)
	content, _ := state["untrusted_content"].(string)
	if n := len([]rune(content)); n != MaxContentChars {
		t.Fatalf("content not capped: %d runes", n)
	}
	if state["url"] != "https://x.test/a" || state["title"] != "A" {
		t.Fatalf("state = %v", state)
	}
}

func TestSuggestedRouteGating(t *testing.T) {
	cases := []struct {
		kind      string
		threshold float64
		want      string
	}{
		{"job_posting", 0.75, "application"},
		{"tender", 0.75, "application"},
		{"person_profile", 0.75, "person"},
		{"job_posting", 0.95, ""}, // jevtest answers p=0.94
		{"docs", 0.5, ""},
		{"job_posting", 0, "application"}, // 0 ⇒ default 0.75
	}
	for _, tc := range cases {
		jevtest.NewServer(t, jevtest.Fixed(map[string]string{"kind": tc.kind}))
		r, err := Classify(context.Background(), newClient(t), Input{URL: "u", Threshold: tc.threshold})
		if err != nil {
			t.Fatal(err)
		}
		if r.SuggestedRoute != tc.want {
			t.Errorf("%s @%v: route %q, want %q", tc.kind, tc.threshold, r.SuggestedRoute, tc.want)
		}
	}
}

func TestClassifyError(t *testing.T) {
	srv := jevtest.NewServer(t, nil)
	srv.SetStatus(400)
	if _, err := Classify(context.Background(), newClient(t), Input{URL: "u"}); err == nil {
		t.Fatal("want error on HTTP 400")
	}
}

func TestWriteReadAndLoadInput(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Read(dir); err != nil {
		t.Fatalf("missing file must not be an error: %v", err)
	}
	if err := Write(dir, Result{Kind: "x"}); err == nil {
		t.Fatal("Write into a non-capture directory must fail")
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Result{Kind: "tender", P: 0.8, Probabilities: map[string]float64{"tender": 0.8}, Model: "m", At: "2026-09-25T00:00:00Z", SuggestedRoute: "application"}
	if err := Write(dir, r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"kind", "p", "probabilities", "model", "at", "suggested_route"} {
		if _, ok := m[k]; !ok {
			t.Errorf("classification.json lacks %q: %s", k, raw)
		}
	}
	got, ok, err := Read(dir)
	if err != nil || !ok || got.Kind != "tender" || got.SuggestedRoute != "application" {
		t.Fatalf("Read = %+v %v %v", got, ok, err)
	}

	env := t.TempDir()
	if err := os.WriteFile(filepath.Join(env, "meta.json"), []byte(`{"url":"https://jobs.test/1","title":"Engineer"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env, "readable.md"), []byte("We are hiring"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := LoadInput(env)
	if err != nil {
		t.Fatal(err)
	}
	if in.URL != "https://jobs.test/1" || in.Title != "Engineer" || in.Content != "We are hiring" {
		t.Fatalf("LoadInput = %+v", in)
	}
	if _, err := LoadInput(t.TempDir()); err == nil {
		t.Fatal("a directory without meta.json is not a capture")
	}
}
