package crawlsite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// Defaults for a crawl that was not told otherwise.
const (
	DefaultMaxDepth = 1
	DefaultMaxPages = 50
	DefaultDelay    = 1 * time.Second
	DefaultTimeout  = 30 * time.Second
	// DefaultMaxCrawlDelay caps what a robots.txt can ask a crawl to wait.
	// A site can slow this crawler down; it cannot park it. "Crawl-delay:
	// 86400" is a day per page, and the crawl it stops is one a person is
	// waiting on.
	DefaultMaxCrawlDelay = 60 * time.Second
	// RequestBudgetFactor turns MaxPages into a request budget when the
	// caller gives no MaxRequests. Pages that are skipped, duplicated or
	// unreadable still cost the site a request, so the budget has to be
	// larger than the page cap — but it has to exist, or one canonical URL
	// repeated across a generated site makes the frontier unbounded.
	RequestBudgetFactor = 20
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
	// AllowPrivateHosts lets the crawl reach loopback, link-local and
	// RFC1918 addresses. Off by default: a crawl walks URLs a page it does
	// not control handed it, and the interesting target on a private
	// address is never the one the operator meant. Turn it on to crawl an
	// intranet, or a test server.
	AllowPrivateHosts bool
	// MaxRequests caps HTTP requests, not saves. MaxPages bounds the
	// output; this bounds the work. Zero derives one from MaxPages.
	MaxRequests int
	// MaxCrawlDelay caps the Crawl-delay a robots.txt can impose. Zero
	// means DefaultMaxCrawlDelay.
	MaxCrawlDelay time.Duration
	// Collection and Tags are written into every envelope's meta.
	Collection string
	Tags       []string

	// Sink receives each finished envelope. Required.
	Sink Sink
	// Client is the HTTP client; nil builds one from Timeout.
	Client *http.Client
	// Now and Sleep exist so a test can run a crawl with a delay without
	// waiting for it. A nil Sleep waits for real, and that wait ends early
	// on a cancelled context; a Sleep supplied here is trusted to return.
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
	// requests counts every page request attempted, which is what the
	// crawl's budget is actually spent on.
	requests int
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
	if opts.MaxRequests <= 0 {
		opts.MaxRequests = opts.MaxPages * RequestBudgetFactor
	}
	if opts.MaxCrawlDelay <= 0 {
		opts.MaxCrawlDelay = DefaultMaxCrawlDelay
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
	base := opts.Client
	if base == nil {
		base = &http.Client{Timeout: opts.Timeout}
	}
	// A copy: the redirect policy and the address guard belong to this
	// crawl, and the caller's client is the caller's.
	client := *base
	c := &Crawler{
		opts:      opts,
		client:    &client,
		scope:     newScope(opts.IncludeSubdomains),
		seen:      map[string]bool{},
		savedKeys: map[string]bool{},
		lastHit:   map[string]time.Time{},
	}
	client.CheckRedirect = c.checkRedirect
	if !opts.AllowPrivateHosts {
		client.Transport = guardTransport(client.Transport, opts.Timeout)
	}
	c.robots = newRobotsCache(c.client, opts.UserAgent)
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
		// MaxPages bounds what is written, which is not the same as what is
		// done: a site where every page declares one canonical URL saves
		// once and keeps handing out links forever. The budget is spent on
		// requests, so a crawl that saves nothing still ends.
		if c.requests >= c.opts.MaxRequests {
			sum.Truncated = true
			break
		}
		it := queue[0]
		queue = queue[1:]

		links, err := c.visit(ctx, it, sum)
		if err != nil {
			if ctx.Err() != nil {
				return sum, ctx.Err()
			}
			continue
		}
		if it.depth >= c.opts.MaxDepth {
			continue
		}
		for _, link := range links {
			if len(c.seen) >= c.opts.MaxRequests {
				// The frontier can never usefully outgrow the budget that
				// would have to be spent to walk it.
				sum.Truncated = true
				break
			}
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
		if u != nil && !rules.allowed(robotsTarget(u)) {
			c.note(sum, &sum.Skipped, it, "disallowed by robots.txt")
			return nil, errSkipped
		}
		if d := rules.crawlDelay; d > 0 {
			if d > c.opts.MaxCrawlDelay {
				d = c.opts.MaxCrawlDelay
			}
			if err := c.waitFor(ctx, hostOf(it.url), d); err != nil {
				return nil, err
			}
		}
	}
	if err := c.waitFor(ctx, hostOf(it.url), c.opts.Delay); err != nil {
		return nil, err
	}
	// Marked before the request, not after: a request that fails still
	// cost the site a connection, and the next one waits for it.
	c.markHit(hostOf(it.url))

	c.requests++
	f, err := c.fetch(ctx, it.url)
	if err != nil {
		if errors.Is(err, errNotHTML) {
			c.note(sum, &sum.Skipped, it, err.Error())
			return nil, errSkipped
		}
		c.noteDetail(sum, &sum.Failed, it, summarize(err), err.Error())
		return nil, err
	}
	sum.Fetched++

	// A redirect can land outside the scope; the links off that page are
	// not this crawl's business even though the request was.
	if f.Normalized != it.url && !c.scope.allows(hostOf(f.Normalized)) {
		c.note(sum, &sum.Skipped, it, "redirected out of scope to "+f.Normalized)
		return nil, errSkipped
	}
	// robots.txt was asked about the URL that was queued. A redirect makes
	// the page that arrived a different URL, under rules that may be a
	// different host's, and it is the page that arrived which gets saved.
	if c.opts.RespectRobots && f.Normalized != it.url {
		rules := c.robots.rulesFor(ctx, f.Normalized)
		if !rules.allowed(robotsTarget(f.URL)) {
			c.note(sum, &sum.Skipped, it, "redirected to "+f.Normalized+", disallowed by robots.txt")
			return nil, errSkipped
		}
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

// waitFor holds off until at least d has passed since the last request to
// host. Sequential and per-host: the simplest thing that is actually
// polite, and the one whose behaviour a site operator can predict.
func (c *Crawler) waitFor(ctx context.Context, host string, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	last, ok := c.lastHit[host]
	if !ok {
		return nil
	}
	wait := d - c.opts.Now().Sub(last)
	if wait <= 0 {
		return nil
	}
	if c.opts.Sleep != nil {
		c.opts.Sleep(wait)
		return ctx.Err()
	}
	// time.Sleep takes no context, so a crawl waiting out a delay could
	// not be interrupted at all — and the delay is a number the site
	// chose. This wait ends when the caller gives up.
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Crawler) markHit(host string) { c.lastHit[host] = c.opts.Now() }

// summarizer is an error that says less in a Summary than it says to the
// operator. The Summary is serialized and can travel — into a report, a
// ticket, a dashboard — and a crawl is walked through URLs someone else
// chose. What a hostname resolved to is the operator's business and not
// the summary reader's: quoting it back confirms, to whoever induced the
// crawl, that a guessed internal name exists and what it answers on.
type summarizer interface{ Summary() string }

// summarize is the reason an error contributes to the Summary.
func summarize(err error) string {
	var s summarizer
	if errors.As(err, &s) {
		return s.Summary()
	}
	return err.Error()
}

// note records one URL the crawl did not save. The Summary gets the flat
// reason; the event, which is what the operator sees on their own
// terminal, gets the whole error.
func (c *Crawler) note(sum *Summary, list *[]Note, it item, reason string) {
	c.noteDetail(sum, list, it, reason, reason)
}

func (c *Crawler) noteDetail(sum *Summary, list *[]Note, it item, reason, detail string) {
	*list = append(*list, Note{URL: it.url, Reason: reason})
	kind := EventSkipped
	if list == &sum.Failed {
		kind = EventFailed
	}
	c.emit(Event{Kind: kind, URL: it.url, Depth: it.depth, Reason: detail})
}

func (c *Crawler) emit(e Event) {
	if c.opts.OnEvent != nil {
		c.opts.OnEvent(e)
	}
}
