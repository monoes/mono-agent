package extension

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// doc.ask and doc.related (RCL-05), against the same fake monomind
// knowledge_test.go builds. What is under test is which argv the handler
// assembles, how it turns search hits into citable passages, and what it
// does when a citation will not resolve.

const searchJSON = `[
  {"kind":"excerpt","id":"e1","filePath":"/inbox/2026-09-20T09-00-00Z-post/readable.md",
   "scope":"global","text":"torque the sprocket to 9 Nm on the bench","score":0.82,
   "chunkIndex":2,"anchor":"3f1a9c7b21de#3200-6400",
   "provenance":{"title":"Sprocket Calibration","canonicalUrl":"https://example.com/post",
                 "capturedAt":"2026-09-20T09:00:00.000Z"}},
  {"kind":"triplet","id":"kg:0","source":"sprocket","relation":"needs","target":"torque"}
]`

const citeJSON = `{"docId":"global:/inbox/x/readable.md",
  "filePath":"/inbox/2026-09-20T09-00-00Z-post/readable.md","scope":"global",
  "chunkIndex":2,"startChar":3200,"endChar":6400,"contentHash":"3f1a9c7b21de99",
  "anchor":"3f1a9c7b21de#3200-6400",
  "passage":"Torque the sprocket to 9 Nm on the bench, then re-check.",
  "quote":"Torque the sprocket to 9 Nm on the bench.",
  "title":"Sprocket Calibration","url":"https://example.com/post",
  "citeUrl":"https://example.com/post#:~:text=Torque%20the%20sprocket",
  "capturedAt":"2026-09-20T09:00:00.000Z"}`

func ask(t *testing.T, srv *Server, q string) (*AskResult, []string) {
	t.Helper()
	var stages []string
	req := &Request{Method: MethodDocAsk, Params: map[string]any{"q": q}}
	got, err := srv.handleDocAsk(context.Background(), req, func(stage, _ string) {
		stages = append(stages, stage)
	})
	if err != nil {
		t.Fatalf("doc.ask: %v", err)
	}
	return got.(*AskResult), stages
}

func TestDocAskCitesItsAnswers(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc search"] = searchJSON
	runner.replies["doc cite"] = citeJSON
	srv := serverWithRunner(t, runner)

	result, stages := ask(t, srv, "how much torque")

	if result.Query != "how much torque" {
		t.Errorf("query = %q", result.Query)
	}
	// The knowledge-graph triplet has no capture behind it, and a panel
	// whose whole point is citations must not show an uncitable row.
	if len(result.Answers) != 1 {
		t.Fatalf("answers = %d, want 1 (the triplet must be dropped)", len(result.Answers))
	}
	a := result.Answers[0]
	if a.Quote != "Torque the sprocket to 9 Nm on the bench." {
		t.Errorf("quote = %q", a.Quote)
	}
	if a.CiteURL == "" || !strings.Contains(a.CiteURL, ":~:text=") {
		t.Errorf("citeUrl = %q, want a text fragment back to the passage", a.CiteURL)
	}
	if a.Title != "Sprocket Calibration" || a.Site != "example.com" {
		t.Errorf("title/site = %q/%q", a.Title, a.Site)
	}
	if a.CapturedAt != "2026-09-20T09:00:00.000Z" {
		t.Errorf("capturedAt = %q", a.CapturedAt)
	}
	if a.Envelope != "/inbox/2026-09-20T09-00-00Z-post" {
		t.Errorf("envelope = %q", a.Envelope)
	}
	// The anchor from the hit is what gets cited, so the quote is the
	// passage the search actually matched.
	if argv := runner.argv("doc cite"); argv == nil ||
		!strings.Contains(strings.Join(argv, " "), "--anchor 3f1a9c7b21de#3200-6400") {
		t.Errorf("cite argv = %v, want it to pass the hit's anchor", argv)
	}
	if strings.Join(stages, ",") != "searching,citing" {
		t.Errorf("progress stages = %v, want searching then citing", stages)
	}
}

// A citation that will not resolve leaves the hit in place with the chunk
// text as its quote. Dropping it would shorten the list with no explanation.
func TestDocAskFallsBackWhenACitationFails(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc search"] = searchJSON
	runner.errs["doc cite"] = errors.New("source file is gone")
	srv := serverWithRunner(t, runner)

	result, _ := ask(t, srv, "how much torque")
	if len(result.Answers) != 1 {
		t.Fatalf("answers = %d, want the hit kept", len(result.Answers))
	}
	if result.Answers[0].Quote != "torque the sprocket to 9 Nm on the bench" {
		t.Errorf("quote = %q, want the chunk text", result.Answers[0].Quote)
	}
	if result.Answers[0].URL != "https://example.com/post" {
		t.Errorf("url = %q, want the hit's own provenance", result.Answers[0].URL)
	}
}

// A stale anchor is reported, never quietly served as if it were current.
func TestDocAskCarriesStaleThrough(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc search"] = searchJSON
	var stale map[string]any
	_ = json.Unmarshal([]byte(citeJSON), &stale)
	stale["stale"] = true
	blob, _ := json.Marshal(stale)
	runner.replies["doc cite"] = string(blob)
	srv := serverWithRunner(t, runner)

	result, _ := ask(t, srv, "how much torque")
	if !result.Answers[0].Stale {
		t.Fatal("a stale citation was served as if it were current")
	}
}

func TestDocAskOnAnEmptyBrain(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc search"] = `[]`
	srv := serverWithRunner(t, runner)

	result, _ := ask(t, srv, "anything")
	if len(result.Answers) != 0 {
		t.Fatalf("answers = %d, want none", len(result.Answers))
	}
	if runner.argv("doc cite") != nil {
		t.Fatal("cited something with no hits")
	}
}

func TestDocAskNeedsAQuestion(t *testing.T) {
	srv := serverWithRunner(t, newFakeRunner())
	req := &Request{Params: map[string]any{"q": "   "}}
	if _, err := srv.handleDocAsk(context.Background(), req, func(string, string) {}); err == nil {
		t.Fatal("an empty question was accepted")
	}
}

// The limit is the extension's, but the ceiling is this side's: each hit
// costs a `doc cite` exec.
func TestDocAskClampsTheLimit(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc search"] = `[]`
	srv := serverWithRunner(t, runner)

	req := &Request{Params: map[string]any{"q": "x", "limit": float64(500)}}
	if _, err := srv.handleDocAsk(context.Background(), req, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(runner.argv("doc search"), " ")
	if !strings.Contains(argv, "--limit 8") {
		t.Fatalf("argv = %s, want the limit clamped to %d", argv, askResultLimit)
	}
}

// `doc related` is documented never to throw, so an unparseable reply means
// an empty brain — not a reason to fail the panel.
func TestDocRelatedTreatsGarbageAsEmpty(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc related"] = "Nothing related yet.\n"
	srv := serverWithRunner(t, runner)

	req := &Request{Params: map[string]any{"url": "https://example.com/post"}}
	got, err := srv.handleDocRelated(context.Background(), req, func(string, string) {})
	if err != nil {
		t.Fatalf("doc.related: %v", err)
	}
	if rows := got.([]map[string]any); len(rows) != 0 {
		t.Fatalf("related = %v, want empty", rows)
	}
}
