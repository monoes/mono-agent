package bottest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Route answers requests whose full URL matches Pattern. Pattern is a glob:
// `*` matches any run of characters (slashes included), `?` one character;
// everything else is literal. Routes are tried in the order given to Serve;
// the first match wins.
type Route struct {
	Pattern string
	// Method, when set, restricts the route to that HTTP method (e.g. "POST").
	Method string
	// File is served as the body (read at request time, relative to the
	// test's working directory — the package directory).
	File string
	// Body is served when File is empty.
	Body string
	// Status defaults to 200.
	Status int
	// ContentType defaults from File's (or the URL's) extension, else
	// "text/html; charset=utf-8".
	ContentType string
	Headers     map[string]string
	// Handler, when set, computes the response and overrides every other
	// response field.
	Handler func(Request) Response
}

// Request is one request the page made.
type Request struct {
	URL          string
	Method       string
	PostData     string
	Headers      map[string]string
	ResourceType string // Document, Script, XHR, Fetch, Image, ...
	// Blocked is true when no Route matched and the request was failed with
	// net::ERR_BLOCKED_BY_CLIENT.
	Blocked bool
}

// Response is what a Route.Handler returns.
type Response struct {
	Status      int
	Body        string
	ContentType string
	Headers     map[string]string
}

// Recorder keeps every request a page made, matched or not.
type Recorder struct {
	mu   sync.Mutex
	reqs []Request
	cond chan struct{}
}

func newRecorder() *Recorder { return &Recorder{cond: make(chan struct{})} }

func (r *Recorder) add(req Request) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	close(r.cond)
	r.cond = make(chan struct{})
	r.mu.Unlock()
}

// Requests returns a copy of every request so far, in arrival order.
func (r *Recorder) Requests() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Request(nil), r.reqs...)
}

// Matching returns the requests whose method equals method ("" = any) and
// whose URL matches the glob pattern.
func (r *Recorder) Matching(method, pattern string) []Request {
	re := globRegexp(pattern)
	var out []Request
	for _, req := range r.Requests() {
		if (method == "" || strings.EqualFold(method, req.Method)) && re.MatchString(req.URL) {
			out = append(out, req)
		}
	}
	return out
}

// Wait blocks up to timeout for a request matching method/pattern (see
// Matching) and returns the first one.
func (r *Recorder) Wait(method, pattern string, timeout time.Duration) (Request, bool) {
	deadline := time.Now().Add(timeout)
	for {
		r.mu.Lock()
		ch := r.cond
		r.mu.Unlock()
		if m := r.Matching(method, pattern); len(m) > 0 {
			return m[0], true
		}
		left := time.Until(deadline)
		if left <= 0 {
			return Request{}, false
		}
		select {
		case <-ch:
		case <-time.After(left):
		}
	}
}

// Reset forgets the recorded requests.
func (r *Recorder) Reset() {
	r.mu.Lock()
	r.reqs = nil
	r.mu.Unlock()
}

var globCache sync.Map

// globRegexp compiles a URL glob (`*` any run, `?` one char) to a regexp.
func globRegexp(pattern string) *regexp.Regexp {
	if re, ok := globCache.Load(pattern); ok {
		return re.(*regexp.Regexp)
	}
	var sb strings.Builder
	sb.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	sb.WriteString("$")
	re := regexp.MustCompile(sb.String())
	globCache.Store(pattern, re)
	return re
}

// MatchGlob reports whether u matches the Route-style glob pattern.
func MatchGlob(pattern, u string) bool { return globRegexp(pattern).MatchString(u) }

// Serve adds routes to the page's fixture table and returns the page's
// Recorder (one per page, shared across Serve calls). Interception is on
// from NewPage: before any Serve call every request is blocked.
func (p *Page) Serve(routes ...Route) *Recorder {
	p.st.mu.Lock()
	p.st.routes = append(p.st.routes, routes...)
	p.st.mu.Unlock()
	return p.st.rec
}

// Recorder returns the page's request recorder.
func (p *Page) Recorder() *Recorder { return p.st.rec }

// SetCSP makes every served Document response carry this
// Content-Security-Policy header ("" removes it). Use it to reproduce sites
// whose CSP blocks `new Function` — and with it ExtensionPage.Eval — e.g.
// "script-src 'self' 'unsafe-inline'".
func (p *Page) SetCSP(policy string) {
	p.st.mu.Lock()
	p.st.csp = policy
	p.st.mu.Unlock()
}

type requestPaused struct {
	RequestID string `json:"requestId"`
	Request   struct {
		URL             string            `json:"url"`
		URLFragment     string            `json:"urlFragment"`
		Method          string            `json:"method"`
		Headers         map[string]string `json:"headers"`
		PostData        string            `json:"postData"`
		PostDataEntries []struct {
			Bytes string `json:"bytes"`
		} `json:"postDataEntries"`
	} `json:"request"`
	ResourceType string `json:"resourceType"`
}

func (st *pageState) handleRequestPaused(raw json.RawMessage) {
	var ev requestPaused
	if err := json.Unmarshal(raw, &ev); err != nil {
		return
	}
	req := Request{
		URL:          ev.Request.URL,
		Method:       ev.Request.Method,
		PostData:     ev.Request.PostData,
		Headers:      ev.Request.Headers,
		ResourceType: ev.ResourceType,
	}
	if req.PostData == "" && len(ev.Request.PostDataEntries) > 0 {
		var sb strings.Builder
		for _, e := range ev.Request.PostDataEntries {
			if b, err := base64.StdEncoding.DecodeString(e.Bytes); err == nil {
				sb.Write(b)
			}
		}
		req.PostData = sb.String()
	}

	st.mu.Lock()
	routes := append([]Route(nil), st.routes...)
	csp := st.csp
	st.mu.Unlock()

	var route *Route
	for i := range routes {
		r := &routes[i]
		if r.Method != "" && !strings.EqualFold(r.Method, req.Method) {
			continue
		}
		if globRegexp(r.Pattern).MatchString(req.URL) {
			route = r
			break
		}
	}
	if route == nil {
		req.Blocked = true
		st.rec.add(req)
		_, _ = st.b.send(st.session, "Fetch.failRequest", map[string]interface{}{
			"requestId": ev.RequestID, "errorReason": "BlockedByClient",
		}, 10*time.Second)
		return
	}
	st.rec.add(req)

	resp, err := route.respond(req)
	if err != nil {
		st.t.Logf("bottest: route %q: %v", route.Pattern, err)
		resp = Response{Status: 500, Body: err.Error(), ContentType: "text/plain"}
	}
	headers := []map[string]string{{"name": "Content-Type", "value": resp.ContentType}}
	for k, v := range resp.Headers {
		headers = append(headers, map[string]string{"name": k, "value": v})
	}
	if csp != "" && ev.ResourceType == "Document" {
		headers = append(headers, map[string]string{"name": "Content-Security-Policy", "value": csp})
	}
	_, _ = st.b.send(st.session, "Fetch.fulfillRequest", map[string]interface{}{
		"requestId":       ev.RequestID,
		"responseCode":    resp.Status,
		"responseHeaders": headers,
		"body":            base64.StdEncoding.EncodeToString([]byte(resp.Body)),
	}, 10*time.Second)
}

func (r *Route) respond(req Request) (Response, error) {
	var resp Response
	if r.Handler != nil {
		resp = r.Handler(req)
	} else {
		resp = Response{Status: r.Status, Body: r.Body, ContentType: r.ContentType, Headers: r.Headers}
		if r.File != "" {
			b, err := os.ReadFile(r.File)
			if err != nil {
				return Response{}, fmt.Errorf("read fixture: %w", err)
			}
			resp.Body = string(b)
		}
	}
	if resp.Status == 0 {
		resp.Status = 200
	}
	if resp.ContentType == "" {
		resp.ContentType = guessType(r.File, req.URL)
	}
	return resp, nil
}

func guessType(file, rawURL string) string {
	ext := path.Ext(file)
	if ext == "" {
		if u, err := url.Parse(rawURL); err == nil {
			ext = path.Ext(u.Path)
		}
	}
	if ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	return "text/html; charset=utf-8"
}
