package crawlsite

import (
	"bytes"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// extracted is everything one fetched page contributes: the provenance
// meta.json wants, the Markdown that gets chunked, and the links the crawl
// walks next.
type extracted struct {
	Title       string
	Canonical   string
	Byline      string
	PublishedAt string
	Favicon     string
	Description string
	Lang        string
	SiteName    string
	Markdown    string
	Links       []string
}

// junkSelectors are the parts of a page that are furniture, not document.
// They are removed before the readable root is chosen, so a site with a
// 400-link footer does not score its footer as the article.
const junkSelectors = "script, style, noscript, template, iframe, svg, form, " +
	"nav, header, footer, aside, [role=navigation], [role=banner], " +
	"[role=complementary], [role=search], [aria-hidden=true], [hidden]"

// extractPage parses one HTML document.
func extractPage(body []byte, pageURL *url.URL) (*extracted, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	base := pageURL
	if href, ok := doc.Find("base[href]").First().Attr("href"); ok {
		if u, perr := url.Parse(strings.TrimSpace(href)); perr == nil && pageURL != nil {
			base = pageURL.ResolveReference(u)
		}
	}

	out := &extracted{
		Title: firstNonEmpty(metaContent(doc, "og:title"), metaContent(doc, "twitter:title"),
			doc.Find("title").First().Text()),
		Byline: firstNonEmpty(metaContent(doc, "author"), metaContent(doc, "article:author"),
			metaContent(doc, "twitter:creator"), doc.Find("[rel=author]").First().Text()),
		PublishedAt: firstNonEmpty(metaContent(doc, "article:published_time"), metaContent(doc, "date"),
			metaContent(doc, "pubdate"), attrOf(doc.Find("time[datetime]").First(), "datetime")),
		Description: firstNonEmpty(metaContent(doc, "og:description"), metaContent(doc, "description")),
		SiteName:    metaContent(doc, "og:site_name"),
		Lang:        strings.TrimSpace(attrOf(doc.Find("html").First(), "lang")),
	}
	out.Title = squashSpace(out.Title)
	out.Byline = squashSpace(out.Byline)
	out.Description = squashSpace(out.Description)

	if href, ok := doc.Find("link[rel=canonical]").First().Attr("href"); ok {
		out.Canonical = resolveAgainst(base, href)
	}
	if out.Canonical == "" {
		out.Canonical = resolveAgainst(base, metaContent(doc, "og:url"))
	}
	out.Favicon = faviconOf(doc, base)
	out.Links = linksOf(doc, base)

	// Strip the furniture from the parsed tree. goquery mutates in place,
	// but every field above has already been read off it.
	doc.Find(junkSelectors).Remove()
	out.Markdown = assembleReadable(out.Title, renderMarkdown(readableRoot(doc), base))
	return out, nil
}

// readableRoot picks the element the article lives in. It is the cheap
// third of what Readability does: score every block of prose onto its
// parent, take the best parent, and fall back to the body when a page has
// no prose worth scoring at all (a link index, a gallery) — which still
// produces a usable document rather than an empty one.
func readableRoot(doc *goquery.Document) *html.Node {
	scores := map[*html.Node]float64{}
	doc.Find("p, pre, blockquote").Each(func(_ int, s *goquery.Selection) {
		n := s.Get(0)
		text := strings.TrimSpace(squashSpace(s.Text()))
		if len(text) < 25 {
			return
		}
		// Commas are a crude but effective proxy for real sentences over
		// navigation text, and it is what Readability itself counts.
		score := 1 + float64(strings.Count(text, ",")) + float64(len(text))/100
		if n.Parent != nil {
			scores[n.Parent] += score
		}
		if n.Parent != nil && n.Parent.Parent != nil {
			scores[n.Parent.Parent] += score / 2
		}
	})
	var best *html.Node
	bestScore := 0.0
	for node, score := range scores {
		// Prefer the higher score; on a tie prefer the shallower node, so
		// the choice does not depend on map iteration order.
		if score > bestScore || (score == bestScore && best != nil && depthOf(node) < depthOf(best)) {
			best, bestScore = node, score
		}
	}
	if best != nil {
		return best
	}
	for _, sel := range []string{"article", "main", "[role=main]", "body"} {
		if s := doc.Find(sel).First(); s.Length() > 0 {
			return s.Get(0)
		}
	}
	return doc.Get(0)
}

func depthOf(n *html.Node) int {
	d := 0
	for cur := n.Parent; cur != nil; cur = cur.Parent {
		d++
	}
	return d
}

// assembleReadable puts the page title at the top of readable.md unless the
// extracted body already opens with it. The title is the one piece of the
// document a chunker cannot recover from the body.
func assembleReadable(title, body string) string {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" {
		return body + "\n"
	}
	first := body
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if strings.EqualFold(strings.TrimSpace(strings.TrimLeft(first, "# ")), title) {
		return body + "\n"
	}
	if body == "" {
		return "# " + title + "\n"
	}
	return "# " + title + "\n\n" + body + "\n"
}

// metaContent reads a <meta> value by property or name, in that order —
// OpenGraph uses property=, everything older uses name=.
func metaContent(doc *goquery.Document, key string) string {
	for _, sel := range []string{
		`meta[property="` + key + `"]`,
		`meta[name="` + key + `"]`,
		`meta[itemprop="` + key + `"]`,
	} {
		if v, ok := doc.Find(sel).First().Attr("content"); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// faviconOf resolves the page's icon, falling back to the well-known
// /favicon.ico that every browser asks for anyway.
func faviconOf(doc *goquery.Document, base *url.URL) string {
	for _, sel := range []string{
		`link[rel="icon"]`, `link[rel="shortcut icon"]`,
		`link[rel="apple-touch-icon"]`, `link[rel="alternate icon"]`,
	} {
		if href, ok := doc.Find(sel).First().Attr("href"); ok {
			if abs := resolveAgainst(base, href); abs != "" {
				return abs
			}
		}
	}
	if base == nil {
		return ""
	}
	return base.Scheme + "://" + base.Host + "/favicon.ico"
}

// linksOf returns every in-document href, resolved and de-duplicated in
// document order.
func linksOf(doc *goquery.Document, base *url.URL) []string {
	seen := map[string]bool{}
	var out []string
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		if rel, ok := s.Attr("rel"); ok && strings.Contains(strings.ToLower(rel), "nofollow") {
			return
		}
		abs := resolveAgainst(base, href)
		if abs == "" || seen[abs] {
			return
		}
		seen[abs] = true
		out = append(out, abs)
	})
	return out
}

// resolveAgainst makes href absolute against base, rejecting the hrefs that
// are not documents (fragments, javascript:, mailto:, data:).
func resolveAgainst(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		u.Fragment, u.RawFragment = "", ""
		return u.String()
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func attrOf(s *goquery.Selection, key string) string {
	v, _ := s.Attr(key)
	return v
}
