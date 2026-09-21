package crawlsite

import (
	"net/url"
	"strings"
	"testing"
)

const articleHTML = `<!doctype html>
<html lang="en">
<head>
  <title>Fallback Title</title>
  <meta property="og:title" content="How Capture Works">
  <meta name="author" content="A. Writer">
  <meta property="article:published_time" content="2026-09-01T10:00:00Z">
  <meta name="description" content="A short summary.">
  <meta property="og:site_name" content="Example Journal">
  <link rel="canonical" href="/articles/how-capture-works">
  <link rel="icon" href="/static/icon.png">
</head>
<body>
  <nav><a href="/nav-only">Navigation</a></nav>
  <article>
    <h1>How Capture Works</h1>
    <p>The first paragraph is long enough to score, and it has commas, several of them, in fact.</p>
    <p>A <a href="/next">link to the next page</a> and <strong>bold</strong> text.</p>
    <ul><li>one</li><li>two</li></ul>
    <pre><code>go build ./...</code></pre>
  </article>
  <footer><a href="https://other.example.org/x">Off site</a></footer>
  <script>console.log("tracking")</script>
</body></html>`

func mustExtract(t *testing.T, body, pageURL string) *extracted {
	t.Helper()
	u, err := url.Parse(pageURL)
	if err != nil {
		t.Fatalf("parse %q: %v", pageURL, err)
	}
	ex, err := extractPage([]byte(body), u)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return ex
}

func TestExtractMetadata(t *testing.T) {
	ex := mustExtract(t, articleHTML, "https://example.com/articles/how-capture-works?utm_source=x")

	if ex.Title != "How Capture Works" {
		t.Errorf("Title = %q, want the og:title", ex.Title)
	}
	if ex.Canonical != "https://example.com/articles/how-capture-works" {
		t.Errorf("Canonical = %q", ex.Canonical)
	}
	if ex.Byline != "A. Writer" {
		t.Errorf("Byline = %q", ex.Byline)
	}
	if ex.PublishedAt != "2026-09-01T10:00:00Z" {
		t.Errorf("PublishedAt = %q", ex.PublishedAt)
	}
	if ex.Favicon != "https://example.com/static/icon.png" {
		t.Errorf("Favicon = %q", ex.Favicon)
	}
	if ex.Description != "A short summary." || ex.Lang != "en" || ex.SiteName != "Example Journal" {
		t.Errorf("description/lang/siteName = %q/%q/%q", ex.Description, ex.Lang, ex.SiteName)
	}
}

func TestExtractLinksAreAbsoluteAndDocumentsOnly(t *testing.T) {
	ex := mustExtract(t, articleHTML+`<a href="mailto:x@y.z">mail</a><a href="#top">top</a>`,
		"https://example.com/articles/how-capture-works")
	want := map[string]bool{
		"https://example.com/nav-only": true,
		"https://example.com/next":     true,
		"https://other.example.org/x":  true,
	}
	for _, l := range ex.Links {
		if !want[l] {
			t.Errorf("unexpected link %q", l)
		}
		delete(want, l)
	}
	if len(want) > 0 {
		t.Errorf("missing links: %v", want)
	}
}

func TestExtractReadableMarkdown(t *testing.T) {
	ex := mustExtract(t, articleHTML, "https://example.com/articles/how-capture-works")
	md := ex.Markdown

	for _, want := range []string{
		"# How Capture Works",
		"The first paragraph is long enough",
		"[link to the next page](https://example.com/next)",
		"**bold**",
		"- one",
		"```",
		"go build ./...",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("readable.md missing %q:\n%s", want, md)
		}
	}
	for _, unwanted := range []string{"console.log", "Navigation", "Off site"} {
		if strings.Contains(md, unwanted) {
			t.Errorf("readable.md still contains furniture %q:\n%s", unwanted, md)
		}
	}
	if strings.Count(md, "# How Capture Works") != 1 {
		t.Errorf("title should appear once, not twice:\n%s", md)
	}
}

func TestExtractFallsBackToBodyWithoutProse(t *testing.T) {
	ex := mustExtract(t, `<html><head><title>Index</title></head><body><a href="/a">a</a></body></html>`,
		"https://example.com/")
	if !strings.Contains(ex.Markdown, "# Index") {
		t.Errorf("a page with no prose should still produce a document:\n%s", ex.Markdown)
	}
	// The title is prepended whatever happens, so asserting "# Index" says
	// nothing about the fallback. What the fallback decides is the root the
	// body is rendered from: without it the whole document is the root, and
	// the <head> leaks in ("Index[a](…)"). Hence the exact document.
	if want := "# Index\n\n[a](https://example.com/a)\n"; ex.Markdown != want {
		t.Errorf("a page with no prose should render its body and nothing else:\ngot  %q\nwant %q",
			ex.Markdown, want)
	}
	if ex.Favicon != "https://example.com/favicon.ico" {
		t.Errorf("Favicon = %q, want the well-known default", ex.Favicon)
	}
}

func TestExtractHonoursBaseHref(t *testing.T) {
	ex := mustExtract(t, `<html><head><base href="https://cdn.example.com/docs/"></head>
		<body><a href="page.html">p</a></body></html>`, "https://example.com/x/y")
	if len(ex.Links) != 1 || ex.Links[0] != "https://cdn.example.com/docs/page.html" {
		t.Errorf("links = %v, want the base-resolved URL", ex.Links)
	}
}
