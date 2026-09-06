package crawl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// FetchOptions controls how FetchPage retrieves a URL.
type FetchOptions struct {
	RenderMode   string        `json:"render_mode"`   // "auto", "static", "browser"
	WaitSelector string        `json:"wait_selector"` // CSS selector to wait for (browser mode)
	Timeout      time.Duration `json:"timeout"`
}

// FetchResult is the output of FetchPage.
type FetchResult struct {
	HTML     string        `json:"html"`
	FinalURL string        `json:"final_url"`
	Mode     string        `json:"mode"`
	Duration time.Duration `json:"duration"`
}

// CleanOptions controls content cleaning behaviour.
type CleanOptions struct {
	KeepNav bool `json:"keep_nav"`
}

// PageContent is the structured output of CleanContent.
type PageContent struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Author      string     `json:"author"`
	PublishedAt string     `json:"published_at"`
	URL         string     `json:"url"`
	Favicon     string     `json:"favicon"`
	MainText    string     `json:"main_text"`
	Markdown    string     `json:"markdown"`
	Links       []Link     `json:"links"`
	Images      []Image    `json:"images"`
	Headings    []Heading  `json:"headings"`
	Tables      [][]string `json:"tables"`
	TokenCount  int        `json:"token_count"`
}

// Link represents a hyperlink extracted from a page.
type Link struct {
	Text       string `json:"text"`
	URL        string `json:"url"`
	IsExternal bool   `json:"external"`
}

// Image represents an image extracted from a page.
type Image struct {
	Alt    string `json:"alt"`
	Src    string `json:"src"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// Heading represents a heading element.
type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

// ---------------------------------------------------------------------------
// Shared HTTP client
// ---------------------------------------------------------------------------

// dnsResolver resolves a hostname to its IP addresses. It is a package
// variable (rather than a direct call to net.DefaultResolver.LookupIPAddr) so
// tests can simulate DNS responses — including DNS rebinding, where the same
// hostname resolves to a different address on a later lookup.
var dnsResolver = net.DefaultResolver.LookupIPAddr

// rawDial performs the actual TCP connection to a resolved, already-validated
// address. It is a package variable so tests can observe/stub the real
// network dial without opening a socket.
var rawDial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext

// httpClient is used for all outbound fetches performed by this package.
//
// The Transport's DialContext is safeDialContext: it resolves the target
// hostname and validates the resulting IP address ITSELF, immediately before
// connecting to that exact IP. This closes a DNS-rebinding SSRF gap where an
// earlier validation pass (e.g. validateFetchURL, or a redirect's
// CheckRedirect check) resolves a hostname to a public IP, but the actual
// connection — performed independently by the transport's own resolver —
// resolves the same hostname to a different, blocked address (loopback,
// private, link-local/metadata, etc.) moments later. By pinning the dial to
// the single resolution that was just validated, there is no second lookup
// for an attacker to race.
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DialContext: safeDialContext,
	},
	// Re-validate every redirect hop's scheme so a URL cannot redirect into a
	// non-http(s) scheme; the actual address-level SSRF check happens in
	// safeDialContext for every hop's connection, including redirects.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return validateFetchURL(req.URL.String())
	},
}

// validateFetchURL rejects URLs that are not plain http(s) or that resolve to a
// non-public address (loopback, private, link-local, unspecified, multicast).
// This is a fast-fail SSRF guard for FetchPage, whose target URL can
// originate from untrusted workflow input. It runs before any network
// request is attempted, and again on every redirect hop.
//
// This check alone is NOT sufficient to prevent SSRF via DNS rebinding, since
// the actual connection is made later by the HTTP transport, which resolves
// the hostname independently. The authoritative, connection-pinning check
// lives in safeDialContext; this function only provides an early, friendly
// error for the common case.
func validateFetchURL(pageURL string) error {
	u, err := url.Parse(pageURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	ipAddrs, err := dnsResolver(context.Background(), host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	for _, ip := range ipAddrs {
		if isBlockedIP(ip.IP) {
			return fmt.Errorf("access to non-public address %s is not allowed", ip.IP)
		}
	}
	return nil
}

// safeDialContext is the SSRF-safe dialer used by httpClient's transport. It
// resolves addr's hostname exactly once, rejects any resolved IP that is not
// a public address, and connects directly to the validated IP — never to a
// hostname re-resolved by a separate step. TLS verification (SNI + cert
// hostname check) still happens against the original hostname, because it is
// performed by http.Transport on top of the connection this returns, using
// the hostname portion of addr, not the IP we dialed.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("safe dial: invalid address %q: %w", addr, err)
	}

	var candidates []net.IP
	if literal := net.ParseIP(host); literal != nil {
		candidates = []net.IP{literal}
	} else {
		ipAddrs, err := dnsResolver(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("safe dial: resolve host %q: %w", host, err)
		}
		for _, ip := range ipAddrs {
			candidates = append(candidates, ip.IP)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("safe dial: no addresses found for host %q", host)
	}

	var lastErr error
	for _, ip := range candidates {
		if isBlockedIP(ip) {
			lastErr = fmt.Errorf("access to non-public address %s is not allowed", ip)
			continue
		}
		conn, err := rawDial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("safe dial: no reachable address for host %q", host)
	}
	return nil, lastErr
}

// isBlockedIP reports whether ip is a non-public address that must not be
// fetched. IsPrivate covers RFC 1918 / RFC 4193; IsLinkLocalUnicast covers
// 169.254.0.0/16 (incl. the 169.254.169.254 cloud metadata endpoint) and
// fe80::/10.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}

// ---------------------------------------------------------------------------
// FetchPage
// ---------------------------------------------------------------------------

// FetchPage retrieves a web page using the specified render mode.
func FetchPage(ctx context.Context, pageURL string, opts FetchOptions) (FetchResult, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.RenderMode == "" {
		opts.RenderMode = "auto"
	}

	if err := validateFetchURL(pageURL); err != nil {
		return FetchResult{}, err
	}

	switch opts.RenderMode {
	case "static", "auto":
		return fetchStatic(ctx, pageURL, opts)
	case "browser":
		return FetchResult{}, fmt.Errorf("render_mode %q is disabled — no browser is ever launched to fetch pages; use render_mode=static", opts.RenderMode)
	default:
		return FetchResult{}, fmt.Errorf("unknown render mode: %s", opts.RenderMode)
	}
}

// maxHTMLBytes bounds how much of a fetched page's response body is read
// into memory. Without this cap, a malicious or misbehaving remote server
// could stream an unbounded response and exhaust process memory. It is a
// package variable (not a const) so tests can lower it to something small
// and deterministic.
var maxHTMLBytes int64 = 20 << 20 // 20MB

func fetchStatic(ctx context.Context, pageURL string, opts FetchOptions) (FetchResult, error) {
	start := time.Now()
	reqCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, pageURL, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; MonoAgent/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	// Read the body through a hard cap so an unbounded/streaming response
	// cannot force unbounded memory growth. Reading one byte past the limit
	// lets us tell a body that was exactly at the cap apart from one that
	// was truncated.
	limited := io.LimitReader(resp.Body, maxHTMLBytes+1)
	bodyBytes, err := io.ReadAll(limited)
	if err != nil {
		return FetchResult{}, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(bodyBytes)) > maxHTMLBytes {
		return FetchResult{}, fmt.Errorf("response body exceeds the %d byte limit", maxHTMLBytes)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(bodyBytes))
	if err != nil {
		return FetchResult{}, fmt.Errorf("parse html: %w", err)
	}

	html, err := doc.Html()
	if err != nil {
		return FetchResult{}, fmt.Errorf("serialize html: %w", err)
	}

	return FetchResult{
		HTML:     html,
		FinalURL: resp.Request.URL.String(),
		Mode:     "static",
		Duration: time.Since(start),
	}, nil
}

// ---------------------------------------------------------------------------
// CleanContent
// ---------------------------------------------------------------------------

// CleanContent extracts structured content from raw HTML.
func CleanContent(rawHTML string, pageURL string, opts CleanOptions) (PageContent, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return PageContent{}, fmt.Errorf("parse html: %w", err)
	}

	base, _ := url.Parse(pageURL)

	pc := PageContent{URL: pageURL}

	// --- Metadata ---
	pc.Title = strings.TrimSpace(doc.Find("title").First().Text())
	if pc.Title == "" {
		pc.Title = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	pc.Description = metaContent(doc, "description")
	pc.Author = metaContent(doc, "author")
	pc.PublishedAt = extractPublishedAt(doc)
	pc.Favicon = resolveURL(base, extractFavicon(doc))
	canonical := doc.Find("link[rel='canonical']").AttrOr("href", "")
	if canonical != "" {
		pc.URL = resolveURL(base, canonical)
	}

	// --- Extract links before removing elements ---
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		resolved := resolveURL(base, href)
		isExt := isExternal(base, resolved)
		pc.Links = append(pc.Links, Link{
			Text:       strings.TrimSpace(s.Text()),
			URL:        resolved,
			IsExternal: isExt,
		})
	})

	// --- Extract images before removing elements ---
	doc.Find("img").Each(func(_ int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		alt, _ := s.Attr("alt")
		w, _ := strconv.Atoi(s.AttrOr("width", "0"))
		h, _ := strconv.Atoi(s.AttrOr("height", "0"))
		pc.Images = append(pc.Images, Image{
			Alt:    alt,
			Src:    resolveURL(base, src),
			Width:  w,
			Height: h,
		})
	})

	// --- Extract headings ---
	for level := 1; level <= 6; level++ {
		tag := fmt.Sprintf("h%d", level)
		lvl := level // capture
		doc.Find(tag).Each(func(_ int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			if text != "" {
				pc.Headings = append(pc.Headings, Heading{Level: lvl, Text: text})
			}
		})
	}

	// --- Extract tables ---
	doc.Find("table").Each(func(_ int, table *goquery.Selection) {
		var rows []string
		table.Find("tr").Each(func(_ int, tr *goquery.Selection) {
			var cells []string
			tr.Find("td, th").Each(func(_ int, td *goquery.Selection) {
				cells = append(cells, strings.TrimSpace(td.Text()))
			})
			if len(cells) > 0 {
				rows = append(rows, strings.Join(cells, "\t"))
			}
		})
		// Flatten to 2D by splitting on tab.
		for _, row := range rows {
			pc.Tables = append(pc.Tables, strings.Split(row, "\t"))
		}
	})

	// --- Remove unwanted elements ---
	doc.Find("script, style, noscript, svg, iframe, link, meta").Remove()
	// Remove hidden elements.
	doc.Find("[style]").Each(func(_ int, s *goquery.Selection) {
		style, _ := s.Attr("style")
		lower := strings.ToLower(style)
		if strings.Contains(lower, "display:none") || strings.Contains(lower, "display: none") ||
			strings.Contains(lower, "visibility:hidden") || strings.Contains(lower, "visibility: hidden") {
			s.Remove()
		}
	})
	doc.Find("[aria-hidden='true']").Remove()

	if !opts.KeepNav {
		doc.Find("nav, header, footer").Remove()
	}

	// --- MainText ---
	bodyText := doc.Find("body").Text()
	pc.MainText = collapseWhitespace(bodyText)

	// --- Markdown + TokenCount ---
	pc.Markdown = ToMarkdown(rawHTML)
	pc.TokenCount = EstimateTokens(pc.Markdown)

	return pc, nil
}

// ---------------------------------------------------------------------------
// Metadata helpers
// ---------------------------------------------------------------------------

func metaContent(doc *goquery.Document, name string) string {
	val := doc.Find(fmt.Sprintf("meta[name='%s']", name)).AttrOr("content", "")
	if val == "" {
		val = doc.Find(fmt.Sprintf("meta[property='%s']", name)).AttrOr("content", "")
	}
	return strings.TrimSpace(val)
}

func extractPublishedAt(doc *goquery.Document) string {
	// Try common meta tags first.
	for _, prop := range []string{
		"article:published_time",
		"datePublished",
		"date",
		"DC.date.issued",
	} {
		val := doc.Find(fmt.Sprintf("meta[property='%s']", prop)).AttrOr("content", "")
		if val == "" {
			val = doc.Find(fmt.Sprintf("meta[name='%s']", prop)).AttrOr("content", "")
		}
		if val != "" {
			return strings.TrimSpace(val)
		}
	}
	// Fallback: <time> element.
	t := doc.Find("time").First()
	if dt, exists := t.Attr("datetime"); exists {
		return strings.TrimSpace(dt)
	}
	return strings.TrimSpace(t.Text())
}

func extractFavicon(doc *goquery.Document) string {
	fav := doc.Find("link[rel='icon']").AttrOr("href", "")
	if fav == "" {
		fav = doc.Find("link[rel='shortcut icon']").AttrOr("href", "")
	}
	if fav == "" {
		fav = "/favicon.ico"
	}
	return fav
}

// ---------------------------------------------------------------------------
// URL helpers
// ---------------------------------------------------------------------------

func resolveURL(base *url.URL, raw string) string {
	if base == nil || raw == "" {
		return raw
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return base.ResolveReference(ref).String()
}

func isExternal(base *url.URL, resolved string) bool {
	if base == nil {
		return false
	}
	u, err := url.Parse(resolved)
	if err != nil {
		return false
	}
	return u.Host != "" && u.Host != base.Host
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

func collapseWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// ---------------------------------------------------------------------------
// ToMarkdown
// ---------------------------------------------------------------------------

// ToMarkdown converts raw HTML to a markdown string.
func ToMarkdown(rawHTML string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return ""
	}
	// Remove noise elements for markdown conversion.
	doc.Find("script, style, noscript, svg, iframe").Remove()

	var buf strings.Builder
	doc.Find("body").Each(func(_ int, body *goquery.Selection) {
		body.Contents().Each(func(_ int, s *goquery.Selection) {
			convertNode(&buf, s)
		})
	})

	result := buf.String()
	// Collapse 3+ newlines into 2.
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

func convertNode(buf *strings.Builder, s *goquery.Selection) {
	if len(s.Nodes) == 0 {
		return
	}

	node := s.Nodes[0]

	// Text node.
	if node.Type == 1 { // html.TextNode
		text := strings.TrimSpace(node.Data)
		if text != "" {
			buf.WriteString(text)
		}
		return
	}

	// Element node.
	if node.Type != 3 { // not html.ElementNode
		// Recurse into children for other node types.
		s.Contents().Each(func(_ int, child *goquery.Selection) {
			convertNode(buf, child)
		})
		return
	}

	tag := strings.ToLower(node.Data)

	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level, _ := strconv.Atoi(tag[1:])
		prefix := strings.Repeat("#", level)
		text := strings.TrimSpace(s.Text())
		if text != "" {
			buf.WriteString("\n\n")
			buf.WriteString(prefix)
			buf.WriteString(" ")
			buf.WriteString(text)
			buf.WriteString("\n\n")
		}

	case "p", "div", "section", "article":
		buf.WriteString("\n\n")
		s.Contents().Each(func(_ int, child *goquery.Selection) {
			convertNode(buf, child)
		})
		buf.WriteString("\n\n")

	case "a":
		href, _ := s.Attr("href")
		text := strings.TrimSpace(s.Text())
		if text != "" {
			buf.WriteString("[")
			buf.WriteString(text)
			buf.WriteString("](")
			buf.WriteString(href)
			buf.WriteString(")")
		}

	case "img":
		alt, _ := s.Attr("alt")
		src, _ := s.Attr("src")
		buf.WriteString("![")
		buf.WriteString(alt)
		buf.WriteString("](")
		buf.WriteString(src)
		buf.WriteString(")")

	case "strong", "b":
		text := strings.TrimSpace(s.Text())
		if text != "" {
			buf.WriteString("**")
			buf.WriteString(text)
			buf.WriteString("**")
		}

	case "em", "i":
		text := strings.TrimSpace(s.Text())
		if text != "" {
			buf.WriteString("*")
			buf.WriteString(text)
			buf.WriteString("*")
		}

	case "br":
		buf.WriteString("\n")

	case "hr":
		buf.WriteString("\n\n---\n\n")

	case "ul":
		buf.WriteString("\n")
		s.Find("> li").Each(func(_ int, li *goquery.Selection) {
			buf.WriteString("- ")
			li.Contents().Each(func(_ int, child *goquery.Selection) {
				convertNode(buf, child)
			})
			buf.WriteString("\n")
		})
		buf.WriteString("\n")

	case "ol":
		buf.WriteString("\n")
		s.Find("> li").Each(func(i int, li *goquery.Selection) {
			buf.WriteString(strconv.Itoa(i + 1))
			buf.WriteString(". ")
			li.Contents().Each(func(_ int, child *goquery.Selection) {
				convertNode(buf, child)
			})
			buf.WriteString("\n")
		})
		buf.WriteString("\n")

	case "blockquote":
		text := strings.TrimSpace(s.Text())
		if text != "" {
			for _, line := range strings.Split(text, "\n") {
				buf.WriteString("> ")
				buf.WriteString(strings.TrimSpace(line))
				buf.WriteString("\n")
			}
		}

	case "pre":
		code := s.Find("code")
		var text string
		if code.Length() > 0 {
			text = code.Text()
		} else {
			text = s.Text()
		}
		buf.WriteString("\n\n```\n")
		buf.WriteString(text)
		buf.WriteString("\n```\n\n")

	case "code":
		// Only inline code; <pre><code> is handled by "pre".
		parent := s.Parent()
		if parent.Length() > 0 && goquery.NodeName(parent) == "pre" {
			buf.WriteString(s.Text())
			return
		}
		buf.WriteString("`")
		buf.WriteString(s.Text())
		buf.WriteString("`")

	case "table":
		convertTable(buf, s)

	case "li":
		// Handled by ul/ol parents. If standalone, just output text.
		s.Contents().Each(func(_ int, child *goquery.Selection) {
			convertNode(buf, child)
		})

	default:
		// Generic: recurse into children.
		s.Contents().Each(func(_ int, child *goquery.Selection) {
			convertNode(buf, child)
		})
	}
}

func convertTable(buf *strings.Builder, table *goquery.Selection) {
	var rows [][]string
	table.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		var row []string
		tr.Find("td, th").Each(func(_ int, td *goquery.Selection) {
			row = append(row, strings.TrimSpace(td.Text()))
		})
		if len(row) > 0 {
			rows = append(rows, row)
		}
	})
	if len(rows) == 0 {
		return
	}

	// Determine max columns.
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	buf.WriteString("\n\n")
	// Header row.
	writeTableRow(buf, rows[0], maxCols)
	// Separator.
	buf.WriteString("|")
	for i := 0; i < maxCols; i++ {
		buf.WriteString(" --- |")
	}
	buf.WriteString("\n")
	// Data rows.
	for _, row := range rows[1:] {
		writeTableRow(buf, row, maxCols)
	}
	buf.WriteString("\n")
}

func writeTableRow(buf *strings.Builder, row []string, maxCols int) {
	buf.WriteString("|")
	for i := 0; i < maxCols; i++ {
		buf.WriteString(" ")
		if i < len(row) {
			buf.WriteString(row[i])
		}
		buf.WriteString(" |")
	}
	buf.WriteString("\n")
}

// ---------------------------------------------------------------------------
// Token helpers
// ---------------------------------------------------------------------------

// EstimateTokens estimates the number of tokens in text (~0.75 tokens per word).
func EstimateTokens(text string) int {
	words := len(strings.Fields(text))
	return int(math.Ceil(float64(words) * 0.75))
}

// TruncateToTokens truncates markdown to approximately maxTokens by word count.
func TruncateToTokens(markdown string, maxTokens int) string {
	// tokens ~ words * 0.75, so max words ~ maxTokens / 0.75
	maxWords := int(math.Ceil(float64(maxTokens) / 0.75))
	words := strings.Fields(markdown)
	if len(words) <= maxWords {
		return markdown
	}
	return strings.Join(words[:maxWords], " ")
}
