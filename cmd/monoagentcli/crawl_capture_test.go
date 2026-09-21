package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/crawlsite"
)

// crawlTestSite serves two linked pages, enough to prove the command wires
// the crawler to the inbox.
func crawlTestSite(t *testing.T) *httptest.Server {
	t.Helper()
	const prose = "<p>A paragraph long enough, with commas, to read as prose.</p>"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Home</title></head><body>` + prose +
			`<a href="/second">second</a></body></html>`))
	})
	mux.HandleFunc("/second", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Second</title></head><body>` + prose + `</body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// runCrawlCmd executes a `crawl` subcommand, returning stdout.
func runCrawlCmd(t *testing.T, cfg *globalConfig, args ...string) (string, string, error) {
	t.Helper()
	if cfg == nil {
		cfg = &globalConfig{}
	}
	cmd := newCrawlCmd(cfg)
	cmd.SetArgs(args)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestCrawlCaptureWritesEnvelopesIntoTheInbox(t *testing.T) {
	srv := crawlTestSite(t)
	inbox := filepath.Join(t.TempDir(), "inbox")

	_, stderr, err := runCrawlCmd(t, nil, "capture", srv.URL+"/",
		"--out", inbox, "--depth", "1", "--delay", "0", "--robots=false",
		"--collection", "docs", "--tag", "seeded")
	if err != nil {
		t.Fatalf("crawl capture: %v\n%s", err, stderr)
	}

	entries, err := capture.List(inbox)
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("inbox holds %d envelopes, want 2:\n%s", len(entries), stderr)
	}
	titles := map[string]bool{}
	for _, e := range entries {
		titles[e.Title] = true
		if !contains(e.Artifacts, capture.ArtifactHTML) || !contains(e.Artifacts, capture.ArtifactReadable) {
			t.Errorf("%s holds %v, want page.html and readable.md", e.Path, e.Artifacts)
		}
	}
	if !titles["Home"] || !titles["Second"] {
		t.Errorf("captured titles %v, want Home and Second", titles)
	}
}

func TestCrawlCaptureJSONSummary(t *testing.T) {
	srv := crawlTestSite(t)
	inbox := filepath.Join(t.TempDir(), "inbox")

	stdout, stderr, err := runCrawlCmd(t, &globalConfig{JSONOutput: true}, "capture", srv.URL+"/",
		"--out", inbox, "--depth", "0", "--delay", "0", "--robots=false")
	if err != nil {
		t.Fatalf("crawl capture --json: %v\n%s", err, stderr)
	}
	var sum crawlsite.Summary
	if err := json.Unmarshal([]byte(stdout), &sum); err != nil {
		t.Fatalf("decode summary %q: %v", stdout, err)
	}
	if len(sum.Pages) != 1 || sum.Pages[0].Title != "Home" {
		t.Fatalf("summary pages = %+v", sum.Pages)
	}
	if sum.Pages[0].Path == "" || !strings.HasPrefix(sum.Pages[0].Path, inbox) {
		t.Errorf("page path %q should be inside %q", sum.Pages[0].Path, inbox)
	}
}

func TestCrawlCaptureRejectsBadFlags(t *testing.T) {
	cases := [][]string{
		{"capture", "https://example.com", "--depth", "-1"},
		{"capture", "https://example.com", "--max-pages", "0"},
		{"capture", "https://example.com", "--delay", "-1s"},
		{"capture", "ftp://example.com/x"},
	}
	for _, args := range cases {
		if _, _, err := runCrawlCmd(t, nil, args...); err == nil {
			t.Errorf("%v should have been refused", args)
		} else if exitCodeFor(err) != 3 {
			t.Errorf("%v exited %d, want 3 (invalid input)", args, exitCodeFor(err))
		}
	}
}

// The browser-rendering `crawl <url>` and `crawl capture <url>` share a
// command name; a URL must still reach the parent.
func TestCrawlParentStillTakesAURL(t *testing.T) {
	cmd := newCrawlCmd(&globalConfig{})
	target, _, err := cmd.Find([]string{"https://example.com/capture"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if target.Name() != "crawl" {
		t.Errorf("a URL routed to %q, want the parent crawl command", target.Name())
	}
	sub, _, err := cmd.Find([]string{"capture", "https://example.com"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if sub.Name() != "capture" {
		t.Errorf("`crawl capture` routed to %q", sub.Name())
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
