package crawlsite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// SourceCrawl is the meta.source value for an envelope this package wrote.
// The extension's counterpart is capture.SourceExtension; both land in the
// same inbox, through the same writer, and differ only here.
const SourceCrawl = "crawl"

// ArtifactHTML is the crawl's byte-fidelity artifact. A crawl has no MHTML
// to write — there is no browser to ask for one — so it keeps the exact
// bytes the server sent instead, which is the same promise: the archive
// outlives the tool that made it.
const ArtifactHTML = "page.html"

// Defaults for a crawl that was not told otherwise.
const (
	DefaultMaxDepth = 1
	DefaultMaxPages = 50
	DefaultDelay    = 1 * time.Second
	DefaultTimeout  = 30 * time.Second
)

// Sink is where finished envelopes go. *capture.Writer satisfies it; tests
// substitute their own.
type Sink interface {
	Write(env *capture.Envelope) (*capture.Result, error)
}

// Options configures one crawl.
type Options struct {
	// Seeds are the URLs the crawl starts from. Their hosts are in scope
	// by construction.
	Seeds []string
	// MaxDepth is how many links from a seed the crawl follows. 0 captures
	// only the seeds themselves.
	MaxDepth int
	// MaxPages caps how many envelopes are written, however deep the site
	// goes. A crawl that hits it stops cleanly and says so.
	MaxPages int
	// Delay is the minimum gap between two requests to the same host. A
	// Crawl-delay in robots.txt raises it but never lowers it.
	Delay time.Duration
	// Timeout bounds a single request.
	Timeout time.Duration
	// MaxBytes bounds a single page's HTML.
	MaxBytes int64
	// UserAgent identifies the crawler to the site and selects the
	// robots.txt group that applies.
	UserAgent string
	// AllowHosts adds hosts to the scope beyond the seeds'.
	AllowHosts []string
	// IncludeSubdomains admits anything under an allowed host.
	IncludeSubdomains bool
	// RespectRobots honours robots.txt. Default true; see the flag's help.
	RespectRobots bool
	// Collection and Tags are written into every envelope's meta.
	Collection string
	Tags       []string

	// Sink receives each finished envelope. Required.
	Sink Sink
	// Client is the HTTP client; nil builds one from Timeout.
	Client *http.Client
	// Now and Sleep exist so a test can run a crawl with a delay without
	// waiting for it.
	Now   func() time.Time
	Sleep func(time.Duration)
	// OnEvent, if set, is called as the crawl progresses.
	OnEvent func(Event)
}

// EventKind classifies a progress event.
type EventKind string

const (
	EventSaved   EventKind = "saved"
	EventSkipped EventKind = "skipped"
	EventFailed  EventKind = "failed"
)

// Event is one thing that happened to one URL.
type Event struct {
	Kind   EventKind
	URL    string
	Depth  int
	Reason string
	Path   string
}

// PageResult is one envelope this crawl wrote.
type PageResult struct {
	URL          string `json:"url"`
	CanonicalURL string `json:"canonicalUrl,omitempty"`
	Title        string `json:"title,omitempty"`
	Depth        int    `json:"depth"`
	Status       int    `json:"status"`
	Path         string `json:"path"`
	Bytes        int64  `json:"bytes"`
}

// Note is a URL the crawl did not save, and why.
type Note struct {
	URL    string `json:"url"`
	Reason string `json:"reason"`
}

// Summary is what a finished crawl reports. It holds one small record per
// page, never a page's bytes: envelopes are written and released one at a
// time, so a 200-page crawl costs one page of memory, not 200.
type Summary struct {
	Seeds      []string     `json:"seeds"`
	Pages      []PageResult `json:"pages"`
	Skipped    []Note       `json:"skipped,omitempty"`
	Failed     []Note       `json:"failed,omitempty"`
	Fetched    int          `json:"fetched"`
	Duplicates int          `json:"duplicates"`
	// Truncated reports that MaxPages stopped the crawl with work left.
	Truncated bool `json:"truncated"`
}

// Crawler walks a site. One Crawler runs one crawl.
type Crawler struct {
	opts   Options
	client *http.Client
	scope  *scope
	robots *robotsCache
	// seen holds every normalized URL already queued, so a link that
	// appears in a navigation bar on every page is fetched once.
	seen map[string]bool
	// savedKeys holds the dedupe key of every envelope written this run:
	// the canonical URL when the page declared one. Two paths that both
	// canonicalize to the article land as one capture.
	savedKeys map[string]bool
	lastHit   map[string]time.Time
	seeds     []item
}

type item struct {
	url      string
	depth    int
	referrer string
	seed     string
}

// New validates options and prepares a crawler.
func New(opts Options) (*Crawler, error) {
	if opts.Sink == nil {
		return nil, errors.New("crawlsite: no sink to write envelopes to")
	}
	if len(opts.Seeds) == 0 {
		return nil, errors.New("crawlsite: no seed URLs")
	}
	if opts.MaxDepth < 0 {
		return nil, fmt.Errorf("crawlsite: depth must not be negative, got %d", opts.MaxDepth)
	}
	if opts.MaxPages <= 0 {
		opts.MaxPages = DefaultMaxPages
	}
	if opts.Delay < 0 {
		return nil, fmt.Errorf("crawlsite: delay must not be negative, got %s", opts.Delay)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if strings.TrimSpace(opts.UserAgent) == "" {
		opts.UserAgent = DefaultUserAgent
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: opts.Timeout}
	}
	c := &Crawler{
		opts:      opts,
		client:    client,
		scope:     newScope(opts.IncludeSubdomains),
		seen:      map[string]bool{},
		savedKeys: map[string]bool{},
		lastHit:   map[string]time.Time{},
	}
	c.robots = newRobotsCache(client, opts.UserAgent)
	for _, h := range opts.AllowHosts {
		c.scope.add(h)
	}
	// Seeds are normalized here rather than in Run, so a URL that cannot be
	// crawled at all is a construction error the caller can classify as bad
	// input instead of a crawl that starts and immediately fails.
	for _, seed := range opts.Seeds {
		norm, err := normalizeURL(seed)
		if err != nil {
			return nil, err
		}
		c.scope.add(hostOf(norm))
		if c.seen[norm] {
			continue
		}
		c.seen[norm] = true
		c.seeds = append(c.seeds, item{url: norm, depth: 0, seed: norm})
	}
	return c, nil
}

// Run walks the site breadth-first and returns what it did. It stops on a
// cancelled context, on MaxPages, or when the frontier empties; an error
// fetching one page is recorded and the crawl continues.
func (c *Crawler) Run(ctx context.Context) (*Summary, error) {
	sum := &Summary{Pages: []PageResult{}}
	queue := append([]item(nil), c.seeds...)
	for _, s := range c.seeds {
		sum.Seeds = append(sum.Seeds, s.url)
	}

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		if len(sum.Pages) >= c.opts.MaxPages {
			sum.Truncated = true
			break
		}
		it := queue[0]
		queue = queue[1:]

		links, err := c.visit(ctx, it, sum)
		if err != nil {
			continue
		}
		if it.depth >= c.opts.MaxDepth {
			continue
		}
		for _, link := range links {
			norm, nerr := normalizeURL(link)
			if nerr != nil || c.seen[norm] {
				continue
			}
			if !c.scope.allows(hostOf(norm)) {
				continue
			}
			if looksBinary(norm) {
				continue
			}
			c.seen[norm] = true
			queue = append(queue, item{url: norm, depth: it.depth + 1, referrer: it.url, seed: it.seed})
		}
	}
	return sum, nil
}

// visit fetches and stores one page, returning the links to follow.
func (c *Crawler) visit(ctx context.Context, it item, sum *Summary) ([]string, error) {
	if c.opts.RespectRobots {
		rules := c.robots.rulesFor(ctx, it.url)
		u, _ := url.Parse(it.url)
		if u != nil && !rules.allowed(u.EscapedPath()) {
			c.note(sum, &sum.Skipped, it, "disallowed by robots.txt")
			return nil, errSkipped
		}
		if rules.crawlDelay > 0 {
			c.waitFor(hostOf(it.url), rules.crawlDelay)
		}
	}
	c.waitFor(hostOf(it.url), c.opts.Delay)
	// Marked before the request, not after: a request that fails still
	// cost the site a connection, and the next one waits for it.
	c.markHit(hostOf(it.url))

	f, err := c.fetch(ctx, it.url)
	if err != nil {
		if errors.Is(err, errNotHTML) {
			c.note(sum, &sum.Skipped, it, err.Error())
			return nil, errSkipped
		}
		c.note(sum, &sum.Failed, it, err.Error())
		return nil, err
	}
	sum.Fetched++

	// A redirect can land outside the scope; the links off that page are
	// not this crawl's business even though the request was.
	if f.Normalized != it.url && !c.scope.allows(hostOf(f.Normalized)) {
		c.note(sum, &sum.Skipped, it, "redirected out of scope to "+f.Normalized)
		return nil, errSkipped
	}

	ex, err := extractPage(f.Body, f.URL)
	if err != nil {
		c.note(sum, &sum.Failed, it, "parse: "+err.Error())
		return nil, err
	}

	key := f.Normalized
	if ex.Canonical != "" {
		if canon, cerr := normalizeURL(ex.Canonical); cerr == nil {
			key = canon
		}
	}
	if c.savedKeys[key] {
		sum.Duplicates++
		c.note(sum, &sum.Skipped, it, "duplicate of "+key)
		return ex.Links, nil
	}

	res, err := c.opts.Sink.Write(c.envelope(f, ex, it, key))
	if err != nil {
		c.note(sum, &sum.Failed, it, "write: "+err.Error())
		return ex.Links, err
	}
	c.savedKeys[key] = true
	page := PageResult{
		URL: f.Normalized, CanonicalURL: key, Title: ex.Title,
		Depth: it.depth, Status: f.Status, Path: res.Path, Bytes: res.Bytes,
	}
	sum.Pages = append(sum.Pages, page)
	c.emit(Event{Kind: EventSaved, URL: f.Normalized, Depth: it.depth, Path: res.Path})
	return ex.Links, nil
}

// errSkipped ends a visit without recording a failure.
var errSkipped = errors.New("skipped")

// envelope maps one fetched page onto the capture envelope contract. The
// artifacts are the bytes the server sent and the Markdown that gets
// chunked; everything else is provenance.
func (c *Crawler) envelope(f *fetched, ex *extracted, it item, canonical string) *capture.Envelope {
	meta := capture.Meta{
		URL:          f.Normalized,
		CanonicalURL: canonical,
		Title:        ex.Title,
		CapturedAt:   c.opts.Now().UTC().Format(time.RFC3339),
		HTTPStatus:   f.Status,
		Favicon:      ex.Favicon,
		Tags:         append([]string{}, c.opts.Tags...),
		Source:       SourceCrawl,
	}
	if ex.Byline != "" {
		byline := ex.Byline
		meta.Byline = &byline
	}
	if ex.PublishedAt != "" {
		published := ex.PublishedAt
		meta.PublishedAt = &published
	}
	if c.opts.Collection != "" {
		collection := c.opts.Collection
		meta.Collection = &collection
	}
	meta.Extra = crawlExtra(f, ex, it)

	env := &capture.Envelope{
		Meta: meta,
		Artifacts: map[string]capture.Artifact{
			ArtifactHTML:             capture.Inline(f.Body),
			capture.ArtifactReadable: capture.Inline([]byte(ex.Markdown)),
		},
	}
	if f.Truncated {
		env.Warnings = append(env.Warnings,
			fmt.Sprintf("%s: page exceeded the %d byte limit and was truncated", f.Normalized, c.opts.MaxBytes))
	}
	return env
}

// crawlExtra is the provenance a crawl knows and the envelope schema has no
// field for. It rides in meta.json verbatim rather than being dropped: how
// a document was reached is part of how much it should be trusted.
func crawlExtra(f *fetched, ex *extracted, it item) map[string]json.RawMessage {
	extra := map[string]json.RawMessage{}
	put := func(key string, value any) {
		if b, err := json.Marshal(value); err == nil {
			extra[key] = b
		}
	}
	if ex.Description != "" {
		put("description", ex.Description)
	}
	if ex.Lang != "" {
		put("lang", ex.Lang)
	}
	if ex.SiteName != "" {
		put("siteName", ex.SiteName)
	}
	put("crawl", map[string]any{
		"seed":        it.seed,
		"depth":       it.depth,
		"referrer":    it.referrer,
		"contentType": f.ContentType,
		"htmlBytes":   len(f.Body),
		"truncated":   f.Truncated,
	})
	return extra
}

// waitFor holds off until at least d has passed since the last request to
// host. Sequential and per-host: the simplest thing that is actually
// polite, and the one whose behaviour a site operator can predict.
func (c *Crawler) waitFor(host string, d time.Duration) {
	if d <= 0 {
		return
	}
	last, ok := c.lastHit[host]
	if !ok {
		return
	}
	if wait := d - c.opts.Now().Sub(last); wait > 0 {
		c.opts.Sleep(wait)
	}
}

func (c *Crawler) markHit(host string) { c.lastHit[host] = c.opts.Now() }

func (c *Crawler) note(sum *Summary, list *[]Note, it item, reason string) {
	*list = append(*list, Note{URL: it.url, Reason: reason})
	kind := EventSkipped
	if list == &sum.Failed {
		kind = EventFailed
	}
	c.emit(Event{Kind: kind, URL: it.url, Depth: it.depth, Reason: reason})
}

func (c *Crawler) emit(e Event) {
	if c.opts.OnEvent != nil {
		c.opts.OnEvent(e)
	}
}
