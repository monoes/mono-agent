package extension

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
)

// The knowledge handlers, against a fake monomind.
//
// Nothing here execs anything: the Runner is the seam, and what is under
// test is the argv the handlers build, the JSON they make of what comes
// back, and — mostly — what they do when the CLI is absent, silent, or
// returns something other than what the contract says.

// fakeRunner stands in for the monomind CLI. `replies` maps the first two
// argv words ("doc list") to the stdout that call should produce.
type fakeRunner struct {
	mu      sync.Mutex
	replies map[string]string
	errs    map[string]error
	calls   [][]string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{replies: map[string]string{}, errs: map[string]error{}}
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	key := subcommand(args)
	if err := f.errs[key]; err != nil {
		return nil, err
	}
	out, ok := f.replies[key]
	if !ok {
		return nil, errors.New("fakeRunner: no reply configured for " + key)
	}
	return []byte(out), nil
}

// argv returns the recorded call for a subcommand, or nil.
func (f *fakeRunner) argv(key string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if subcommand(c) == key {
			return c
		}
	}
	return nil
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func subcommand(args []string) string {
	if len(args) >= 2 {
		return args[0] + " " + args[1]
	}
	return strings.Join(args, " ")
}

// serverWithRunner is a Server that never listens — the handlers are called
// directly, which is all these tests need.
func serverWithRunner(t *testing.T, runner Runner) *Server {
	t.Helper()
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	srv.SetKnowledgeRunner(runner)
	return srv
}

func lookup(t *testing.T, srv *Server, url string) *SavedDocument {
	t.Helper()
	req := &Request{Method: MethodDocLookup, Params: map[string]any{"url": url}}
	got, err := srv.handleDocLookup(context.Background(), req, func(string, string) {})
	if err != nil {
		t.Fatalf("doc.lookup: %v", err)
	}
	doc, ok := got.(*SavedDocument)
	if !ok {
		t.Fatalf("doc.lookup returned %T, want *SavedDocument", got)
	}
	return doc
}

const libraryJSON = `[
  {"filePath":"/home/u/.monomind/inbox/2026-09-20T09-00-00Z-post/readable.md",
   "scope":"global","title":"Sprocket Calibration",
   "url":"https://example.com/post","site":"example.com",
   "capturedAt":"2026-09-20T09:00:00.000Z","indexedAt":"2026-09-20T09:00:05.000Z",
   "source":"extension","collection":"bench",
   "tags":["mechanics","reading"],"chunkCount":6,"size":4096,"version":3,"captured":true},
  {"filePath":"/home/u/notes/unrelated.md","scope":"shared","title":"Unrelated",
   "capturedAt":"","indexedAt":"2026-09-01T00:00:00.000Z","tags":[],
   "chunkCount":1,"size":10,"captured":false}
]`

const lookupJSON = `{
  "saved": true, "url": "https://example.com/post", "title": "Sprocket Calibration",
  "site": "example.com", "capturedAt": "2026-09-20T09:00:00.000Z", "versions": 3,
  "note": "torque figures worth keeping", "tags": ["mechanics","reading"],
  "collection": "bench", "source": "extension",
  "filePath": "/home/u/.monomind/inbox/2026-09-20T09-00-00Z-post/readable.md",
  "envelope": "/home/u/.monomind/inbox/2026-09-20T09-00-00Z-post", "highlights": 2
}`

// `doc lookup` is the direct answer and the only one that carries the note.
func TestDocLookupPrefersTheLookupCommand(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc lookup"] = lookupJSON
	srv := serverWithRunner(t, runner)

	doc := lookup(t, srv, "https://example.com/post#section-3")

	if !doc.Saved {
		t.Fatal("a saved page was reported as not saved")
	}
	if doc.Note != "torque figures worth keeping" {
		t.Errorf("note = %q — the note is the whole difference from a bookmark", doc.Note)
	}
	if doc.Versions != 3 || doc.Highlights != 2 {
		t.Errorf("versions/highlights = %d/%d, want 3/2", doc.Versions, doc.Highlights)
	}
	if doc.Envelope != "/home/u/.monomind/inbox/2026-09-20T09-00-00Z-post" {
		t.Errorf("envelope = %q", doc.Envelope)
	}
	// The fragment is stripped before the URL ever reaches the CLI, and the
	// URL goes last, behind the `--` (see runDoc).
	argv := runner.argv("doc lookup")
	if argv == nil || argv[len(argv)-1] != "https://example.com/post" {
		t.Fatalf("argv = %v, want the identity URL", argv)
	}
	if runner.argv("doc list") != nil {
		t.Error("fell back to doc list even though doc lookup answered")
	}
}

// An older monomind has no `doc lookup`; the badge must still work.
func TestDocLookupFallsBackToTheLibrary(t *testing.T) {
	runner := newFakeRunner()
	runner.errs["doc lookup"] = errors.New("unknown subcommand: lookup")
	runner.replies["doc list"] = libraryJSON
	srv := serverWithRunner(t, runner)

	doc := lookup(t, srv, "https://example.com/post")

	if !doc.Saved || doc.Versions != 3 {
		t.Fatalf("fallback did not find the page: %+v", doc)
	}
	// The global brain is asked first: that is where extension captures land.
	argv := runner.argv("doc list")
	if argv == nil || strings.Join(argv, " ") != "doc list --global --json" {
		t.Errorf("argv = %v, want the global store asked first", argv)
	}
	// --limit would apply AFTER the newest-first sort and hide exactly the
	// old captures this is looking for.
	if strings.Contains(strings.Join(argv, " "), "--limit") {
		t.Error("the fallback must not limit the library")
	}
	// --text is not a flag `doc list` has; passing one would be a guess.
	if strings.Contains(strings.Join(argv, " "), "--text") {
		t.Error("doc list has no --text filter")
	}
}

// The project store is only consulted when the global one has nothing, so
// the common case stays at one exec.
func TestDocLookupFallbackAsksBothStores(t *testing.T) {
	runner := newFakeRunner()
	runner.errs["doc lookup"] = errors.New("unknown subcommand: lookup")
	runner.replies["doc list"] = `[]`
	srv := serverWithRunner(t, runner)

	if doc := lookup(t, srv, "https://example.com/post"); doc.Saved {
		t.Fatal("reported as saved with an empty library")
	}
	var stores []string
	for _, call := range runner.calls {
		if subcommand(call) == "doc list" {
			stores = append(stores, strings.Join(call, " "))
		}
	}
	if len(stores) != 2 || stores[0] != "doc list --global --json" || stores[1] != "doc list --json" {
		t.Fatalf("stores asked = %v, want global then project", stores)
	}
}

func TestDocLookupFindsASavedPage(t *testing.T) {
	runner := newFakeRunner()
	runner.errs["doc lookup"] = errors.New("unknown subcommand: lookup")
	runner.replies["doc list"] = libraryJSON
	srv := serverWithRunner(t, runner)

	// The tab's URL carries a fragment; the brain keys on the identity
	// URL, which is that URL without it.
	doc := lookup(t, srv, "https://example.com/post#section-3")

	if !doc.Saved {
		t.Fatal("a page that is in the library was reported as not saved")
	}
	if doc.URL != "https://example.com/post" {
		t.Errorf("lookup url = %q, want the fragment stripped", doc.URL)
	}
	if doc.Title != "Sprocket Calibration" || doc.Site != "example.com" {
		t.Errorf("title/site = %q/%q", doc.Title, doc.Site)
	}
	if doc.Versions != 3 {
		t.Errorf("versions = %d, want 3", doc.Versions)
	}
	// No note on this path: `doc list`'s row shape has none. It arrives
	// with `doc lookup`, above.
	if doc.Note != "" {
		t.Errorf("note = %q, want empty on the library fallback", doc.Note)
	}
	if strings.Join(doc.Tags, ",") != "mechanics,reading" {
		t.Errorf("tags = %v", doc.Tags)
	}
	if doc.Collection != "bench" || doc.Source != "extension" {
		t.Errorf("collection/source = %q/%q", doc.Collection, doc.Source)
	}
	// "Open the archived copy" opens the envelope directory, not the
	// readable.md inside it.
	if doc.Envelope != "/home/u/.monomind/inbox/2026-09-20T09-00-00Z-post" {
		t.Errorf("envelope = %q", doc.Envelope)
	}

	argv := runner.argv("doc list")
	if argv == nil {
		t.Fatal("doc list was never called")
	}
	if !strings.Contains(strings.Join(argv, " "), "--json") {
		t.Errorf("argv did not ask for JSON: %v", argv)
	}
}

// `--text` is a SUBSTRING filter, so a row it returns is a candidate, not
// an answer. A different page whose URL merely contains this one's must not
// light the badge.
func TestDocLookupRejectsASubstringMatch(t *testing.T) {
	runner := newFakeRunner()
	runner.errs["doc lookup"] = errors.New("unknown subcommand: lookup")
	runner.replies["doc list"] = `[{"filePath":"/x/readable.md","scope":"global",
	  "title":"Longer","url":"https://example.com/post/comments","site":"example.com",
	  "capturedAt":"2026-09-20T09:00:00.000Z","indexedAt":"","tags":[],
	  "chunkCount":1,"size":1,"version":1,"captured":true}]`
	srv := serverWithRunner(t, runner)

	if doc := lookup(t, srv, "https://example.com/post"); doc.Saved {
		t.Fatalf("a substring match was reported as saved: %+v", doc)
	}
}

func TestDocLookupUnsavedPage(t *testing.T) {
	runner := newFakeRunner()
	runner.errs["doc lookup"] = errors.New("unknown subcommand: lookup")
	runner.replies["doc list"] = `[]`
	srv := serverWithRunner(t, runner)

	doc := lookup(t, srv, "https://example.com/never-seen")
	if doc.Saved {
		t.Fatal("an unsaved page was reported as saved")
	}
	if doc.URL != "https://example.com/never-seen" {
		t.Errorf("url = %q", doc.URL)
	}
}

// A chrome:// or about: tab is not a page that could have been captured.
// Asking about one would spend a Node startup on every new tab.
func TestDocLookupSkipsNonWebURLs(t *testing.T) {
	runner := newFakeRunner()
	srv := serverWithRunner(t, runner)

	for _, raw := range []string{"chrome://newtab", "about:blank", "file:///etc/hosts", ""} {
		req := &Request{Params: map[string]any{"url": raw}}
		got, err := srv.handleDocLookup(context.Background(), req, func(string, string) {})
		if raw == "" {
			if err == nil {
				t.Errorf("an empty url was accepted")
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if doc := got.(*SavedDocument); doc.Saved {
			t.Errorf("%s reported as saved", raw)
		}
	}
	if runner.callCount() != 0 {
		t.Fatalf("monomind was run %d times for non-web URLs", runner.callCount())
	}
}

// monomind missing is not an error the user did anything about. It has to
// arrive as Unavailable so the popup shows an absence, not a red box.
func TestDocLookupReportsAMissingMonomindAsUnavailable(t *testing.T) {
	runner := newFakeRunner()
	runner.errs["doc lookup"] = Unavailable("monomind not found (AI engine)")
	runner.errs["doc list"] = Unavailable("monomind not found (AI engine)")
	srv := serverWithRunner(t, runner)

	req := &Request{Params: map[string]any{"url": "https://example.com/post"}}
	_, err := srv.handleDocLookup(context.Background(), req, func(string, string) {})
	if err == nil {
		t.Fatal("a missing monomind was not reported")
	}
	var re *RequestError
	if !asRequestError(err, &re) || re.Code != CodeUnavailable {
		t.Fatalf("err = %v, want code %q", err, CodeUnavailable)
	}
	// No point falling back: a missing binary fails the same way, slower.
	if runner.argv("doc list") != nil {
		t.Error("fell back to doc list with no monomind installed")
	}
}

// A CLI that prints a human notice instead of JSON must fail loudly rather
// than be read as "nothing is saved" — the badge's silence would otherwise
// be indistinguishable from a broken install.
func TestDocLookupRejectsNonJSON(t *testing.T) {
	runner := newFakeRunner()
	runner.errs["doc lookup"] = errors.New("unknown subcommand: lookup")
	runner.replies["doc list"] = "No documents indexed yet.\n"
	srv := serverWithRunner(t, runner)

	req := &Request{Params: map[string]any{"url": "https://example.com/post"}}
	if _, err := srv.handleDocLookup(context.Background(), req, func(string, string) {}); err == nil {
		t.Fatal("a non-JSON reply was accepted")
	}
}

func TestEnvelopeDir(t *testing.T) {
	cases := map[string]string{
		"/inbox/2026-09-20T09-00-00Z-post/readable.md": "/inbox/2026-09-20T09-00-00Z-post",
		`C:\inbox\post\readable.md`:                    `C:\inbox\post`,
		"readable.md":                                  "",
		"":                                             "",
	}
	for in, want := range cases {
		if got := envelopeDir(in); got != want {
			t.Errorf("envelopeDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentityURLMatchesTheIngestSide(t *testing.T) {
	// Same rule as captureIdentityUrl in capture-envelope.ts: the
	// fragment goes, and nothing else is rewritten — query strings and
	// trailing slashes genuinely distinguish pages.
	cases := map[string]string{
		"https://example.com/post#section-3": "https://example.com/post",
		"https://example.com/post?ref=hn":    "https://example.com/post?ref=hn",
		"https://example.com/post/":          "https://example.com/post/",
		"  https://example.com/post  ":       "https://example.com/post",
		"https://example.com/p#a#b":          "https://example.com/p",
	}
	for in, want := range cases {
		if got := identityURL(in); got != want {
			t.Errorf("identityURL(%q) = %q, want %q", in, got, want)
		}
	}
}
