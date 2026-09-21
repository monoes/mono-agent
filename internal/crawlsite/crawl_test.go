package crawlsite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// testSite serves a small, fixed site: enough shape to exercise depth,
// scope, dedupe, content types and robots.txt without a network.
func testSite(t *testing.T) *httptest.Server {
	t.Helper()
	page := func(title, body string) string {
		return "<!doctype html><html><head><title>" + title + "</title></head><body>" + body + "</body></html>"
	}
	prose := "<p>This is a paragraph with enough words, and commas, to score as prose.</p>"

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page("Index", prose+
			`<a href="/a">A</a><a href="/b">B</a><a href="/dup">Dup</a>`+
			`<a href="/manual.pdf">PDF</a><a href="/logo.png">PNG</a><a href="/data">Data</a>`+
			`<a href="/private/secret">Secret</a>`+
			`<a href="https://elsewhere.example.org/x">Off site</a>`)))
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page("Page A", prose)))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page("Page B", prose+`<a href="/c">C</a>`)))
	})
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page("Page C", prose)))
	})
	mux.HandleFunc("/dup", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Page A</title>` +
			`<link rel="canonical" href="/a"></head><body>` + prose + `</body></html>`))
	})
	// No extension to give it away: only the Content-Type says this is not
	// a document, so it costs a request and is skipped on the response.
	mux.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"not":"html"}`))
	})
	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
	})
	mux.HandleFunc("/private/secret", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page("Secret", prose)))
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /private\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// runCrawl crawls srv with opts applied on top of test-friendly defaults,
// and returns the summary and the inbox the envelopes landed in.
func runCrawl(t *testing.T, srv *httptest.Server, tune func(*Options)) (*Summary, string) {
	t.Helper()
	inbox := filepath.Join(t.TempDir(), "inbox")
	opts := Options{
		Seeds:         []string{srv.URL + "/"},
		MaxDepth:      1,
		MaxPages:      50,
		Delay:         0,
		RespectRobots: true,
		Sink:          &capture.Writer{Inbox: inbox},
		Client:        srv.Client(),
		Sleep:         func(time.Duration) {},
		// The test site is an httptest server on 127.0.0.1.
		AllowPrivateHosts: true,
	}
	if tune != nil {
		tune(&opts)
	}
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sum, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return sum, inbox
}

func savedTitles(sum *Summary) []string {
	out := make([]string, 0, len(sum.Pages))
	for _, p := range sum.Pages {
		out = append(out, p.Title)
	}
	sort.Strings(out)
	return out
}

func TestCrawlDepthZeroCapturesOnlyTheSeed(t *testing.T) {
	srv := testSite(t)
	sum, inbox := runCrawl(t, srv, func(o *Options) { o.MaxDepth = 0 })

	if got := savedTitles(sum); len(got) != 1 || got[0] != "Index" {
		t.Fatalf("saved %v, want only the seed", got)
	}
	entries, err := capture.List(inbox)
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox has %d envelopes, want 1", len(entries))
	}
}

func TestCrawlFollowsLinksToDepth(t *testing.T) {
	srv := testSite(t)
	sum, _ := runCrawl(t, srv, func(o *Options) { o.MaxDepth = 1 })

	got := savedTitles(sum)
	want := []string{"Index", "Page A", "Page B"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("depth 1 saved %v, want %v", got, want)
	}

	sum2, _ := runCrawl(t, srv, func(o *Options) { o.MaxDepth = 2 })
	if got := savedTitles(sum2); len(got) != 4 || got[3] != "Page C" {
		t.Fatalf("depth 2 saved %v, want Page C as well", got)
	}
}

func TestCrawlStopsAtMaxPages(t *testing.T) {
	srv := testSite(t)
	sum, _ := runCrawl(t, srv, func(o *Options) { o.MaxDepth = 3; o.MaxPages = 2 })
	if len(sum.Pages) != 2 {
		t.Fatalf("saved %d pages, want the cap of 2", len(sum.Pages))
	}
	if !sum.Truncated {
		t.Error("a crawl stopped by the cap should say it was truncated")
	}
}

func TestCrawlSkipsNonHTMLAndOffScopeAndRobots(t *testing.T) {
	srv := testSite(t)
	sum, _ := runCrawl(t, srv, nil)

	reasons := map[string]string{}
	for _, n := range sum.Skipped {
		reasons[n.URL] = n.Reason
	}
	if r, ok := reasons[srv.URL+"/data"]; !ok || !strings.Contains(r, "not an HTML document") {
		t.Errorf("a JSON response should be skipped as non-HTML, got %q (skipped: %v)", r, sum.Skipped)
	}
	if r, ok := reasons[srv.URL+"/private/secret"]; !ok || !strings.Contains(r, "robots.txt") {
		t.Errorf("robots-disallowed page should be skipped, got %q", r)
	}
	for _, p := range sum.Pages {
		if strings.Contains(p.URL, "elsewhere.example.org") {
			t.Errorf("off-scope host was crawled: %s", p.URL)
		}
		if strings.HasSuffix(p.URL, ".pdf") {
			t.Errorf("binary link was fetched: %s", p.URL)
		}
	}
	// A link whose extension gives it away is never requested at all, so it
	// is not even a skip note.
	for _, u := range []string{srv.URL + "/manual.pdf", srv.URL + "/logo.png"} {
		if _, ok := reasons[u]; ok {
			t.Errorf("a link that is obviously binary should not cost a request: %s", u)
		}
	}
}

func TestCrawlIgnoresRobotsWhenTurnedOff(t *testing.T) {
	srv := testSite(t)
	sum, _ := runCrawl(t, srv, func(o *Options) { o.RespectRobots = false })
	for _, p := range sum.Pages {
		if strings.HasSuffix(p.URL, "/private/secret") {
			return
		}
	}
	t.Fatalf("with --robots=false the disallowed page should be captured, saved: %v", savedTitles(sum))
}

func TestCrawlDeduplicatesByCanonicalURL(t *testing.T) {
	srv := testSite(t)
	sum, inbox := runCrawl(t, srv, nil)

	if sum.Duplicates != 1 {
		t.Errorf("Duplicates = %d, want 1 (/dup canonicalizes to /a)", sum.Duplicates)
	}
	entries, err := capture.List(inbox)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	seen := map[string]int{}
	for _, e := range entries {
		seen[e.URL]++
	}
	if seen[srv.URL+"/a"] != 1 {
		t.Errorf("/a landed %d times, want exactly one envelope", seen[srv.URL+"/a"])
	}
}

func TestCrawlEnvelopeMatchesTheContract(t *testing.T) {
	srv := testSite(t)
	sum, _ := runCrawl(t, srv, func(o *Options) {
		o.MaxDepth = 0
		o.Collection = "research"
		o.Tags = []string{"crawl", "example"}
	})
	if len(sum.Pages) != 1 {
		t.Fatalf("want one page, got %d", len(sum.Pages))
	}
	dir := sum.Pages[0].Path

	blob, err := os.ReadFile(filepath.Join(dir, capture.MetaFile))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var meta capture.Meta
	if err := json.Unmarshal(blob, &meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if meta.Source != capture.SourceCrawl {
		t.Errorf("meta.source = %q, want %q", meta.Source, capture.SourceCrawl)
	}
	if meta.HTTPStatus != 200 {
		t.Errorf("meta.httpStatus = %d", meta.HTTPStatus)
	}
	if meta.CanonicalURL == "" || meta.URL == "" {
		t.Errorf("meta must carry both urls, got %q / %q", meta.URL, meta.CanonicalURL)
	}
	if meta.ContentHash == "" || !strings.HasPrefix(meta.ContentHash, "sha256:") {
		t.Errorf("meta.contentHash = %q", meta.ContentHash)
	}
	if meta.Collection == nil || *meta.Collection != "research" {
		t.Errorf("meta.collection = %v", meta.Collection)
	}
	if strings.Join(meta.Tags, ",") != "crawl,example" {
		t.Errorf("meta.tags = %v", meta.Tags)
	}
	if _, ok := meta.Extra["crawl"]; !ok {
		t.Errorf("crawl provenance missing from meta: %v", meta.Extra)
	}

	for _, name := range []string{capture.ArtifactHTML, capture.ArtifactReadable} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("envelope is missing %s: %v", name, err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
	md, err := os.ReadFile(filepath.Join(dir, capture.ArtifactReadable))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "# Index") {
		t.Errorf("readable.md missing the title:\n%s", md)
	}
}

// fakeClock advances on every reading, so the elapsed-time arithmetic in
// waitFor is actually exercised: a frozen clock makes every subtraction a
// no-op and an unconditional sleep indistinguishable from a polite one.
type fakeClock struct {
	now  time.Time
	tick time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.now = c.now.Add(c.tick)
	return c.now
}

func (c *fakeClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

func TestCrawlIsPoliteBetweenRequests(t *testing.T) {
	srv := testSite(t)
	clock := &fakeClock{now: time.Unix(0, 0), tick: 250 * time.Millisecond}
	var slept []time.Duration
	_, _ = runCrawl(t, srv, func(o *Options) {
		o.MaxDepth = 1
		o.Delay = 2 * time.Second
		o.Now = clock.Now
		o.Sleep = func(d time.Duration) {
			slept = append(slept, d)
			clock.Sleep(d)
		}
	})
	if len(slept) == 0 {
		t.Fatal("a crawl with a delay should have waited between requests")
	}
	for _, d := range slept {
		if d <= 0 || d > 2*time.Second {
			t.Errorf("waited %s, want a positive wait no longer than the 2s delay", d)
		}
		if d == 2*time.Second {
			t.Errorf("waited the full %s without subtracting the time already elapsed", d)
		}
	}
}

func TestWaitForSubtractsTimeAlreadyElapsed(t *testing.T) {
	now := time.Unix(1000, 0)
	var slept []time.Duration
	c := &Crawler{
		opts: Options{
			Now:   func() time.Time { return now },
			Sleep: func(d time.Duration) { slept = append(slept, d) },
		},
		lastHit: map[string]time.Time{},
	}
	// Nothing fetched from this host yet: no wait at all.
	if err := c.waitFor(context.Background(), "a.test", 2*time.Second); err != nil {
		t.Fatalf("waitFor: %v", err)
	}
	if len(slept) != 0 {
		t.Errorf("the first request to a host waits for nothing, slept %v", slept)
	}
	// Half a second ago: wait out the remaining 1.5s, not the whole delay.
	c.lastHit["a.test"] = now.Add(-500 * time.Millisecond)
	if err := c.waitFor(context.Background(), "a.test", 2*time.Second); err != nil {
		t.Fatalf("waitFor: %v", err)
	}
	if len(slept) != 1 || slept[0] != 1500*time.Millisecond {
		t.Errorf("slept %v, want one wait of 1.5s", slept)
	}
	// Longer ago than the delay: no wait.
	c.lastHit["a.test"] = now.Add(-3 * time.Second)
	if err := c.waitFor(context.Background(), "a.test", 2*time.Second); err != nil {
		t.Fatalf("waitFor: %v", err)
	}
	if len(slept) != 1 {
		t.Errorf("a host last hit longer ago than the delay needs no wait, slept %v", slept)
	}
}

func TestNewRejectsBadOptions(t *testing.T) {
	sink := &capture.Writer{Inbox: t.TempDir()}
	if _, err := New(Options{Sink: sink}); err == nil {
		t.Error("a crawl with no seeds should be refused")
	}
	if _, err := New(Options{Seeds: []string{"https://x.test/"}}); err == nil {
		t.Error("a crawl with no sink should be refused")
	}
	if _, err := New(Options{Seeds: []string{"https://x.test/"}, Sink: sink, MaxDepth: -1}); err == nil {
		t.Error("a negative depth should be refused")
	}
}

func TestNewRejectsUnsupportedSeed(t *testing.T) {
	if _, err := New(Options{Seeds: []string{"ftp://example.com/x"}, Sink: &capture.Writer{Inbox: t.TempDir()}}); err == nil {
		t.Error("an ftp seed should be refused before the crawl starts")
	}
}
