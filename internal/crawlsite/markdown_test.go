package crawlsite

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func render(t *testing.T, fragment string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader("<html><body>" + fragment + "</body></html>"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	base, _ := url.Parse("https://example.com/dir/page")
	var body *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if body != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	return renderMarkdown(body, base)
}

func TestMarkdownBlocks(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<h2>Heading</h2>", "## Heading"},
		{"<p>Hello  \n world</p>", "Hello world"},
		{"<hr>", "---"},
		{"<blockquote><p>quoted</p></blockquote>", "> quoted"},
		{"<ol><li>first</li><li>second</li></ol>", "1. first\n2. second"},
		{"<ul><li>a<ul><li>b</li></ul></li></ul>", "- a\n  - b"},
		{`<a href="/x">text</a>`, "[text](https://example.com/x)"},
		{`<img src="pic.png" alt="a pic">`, "![a pic](https://example.com/dir/pic.png)"},
		{"<p>a <em>b</em> <strong>c</strong> <code>d</code></p>", "a *b* **c** `d`"},
		{"<table><tr><th>h1</th><th>h2</th></tr><tr><td>a</td><td>b</td></tr></table>",
			"| h1 | h2 |\n| --- | --- |\n| a | b |"},
	}
	for _, tc := range cases {
		// Exact, not Contains: "### H" contains "## H", so a converter that
		// rendered every heading one level too deep would pass a substring
		// check while producing the wrong document.
		if got := strings.TrimSpace(render(t, tc.in)); got != tc.want {
			t.Errorf("render(%q) =\n%q\nwant\n%q", tc.in, got, tc.want)
		}
	}
}

func TestMarkdownDropsScriptsAndEmptyLinks(t *testing.T) {
	got := render(t, `<p>keep</p><script>bad()</script><style>.x{}</style>`+
		`<a href="javascript:bad()">js</a><a href="#top">frag</a>`)
	for _, unwanted := range []string{"bad()", ".x{", "javascript:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("markdown kept %q:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "keep") {
		t.Errorf("markdown lost the real content:\n%s", got)
	}
	// A link that resolves to nothing still contributes its text.
	if !strings.Contains(got, "js") || !strings.Contains(got, "frag") {
		t.Errorf("link text should survive a dropped href:\n%s", got)
	}
}

func TestMarkdownLazyImage(t *testing.T) {
	got := render(t, `<img data-src="/lazy.png" alt="late">`)
	if !strings.Contains(got, "![late](https://example.com/lazy.png)") {
		t.Errorf("lazy-loaded image not recovered:\n%s", got)
	}
}

func TestMarkdownNoRunsOfBlankLines(t *testing.T) {
	// No fragment reaches the collapse through renderMarkdown today — every
	// block writer ends its output with exactly one blank line — so the
	// function is tested directly rather than through a fixture that
	// passes whether or not the collapse does anything.
	for _, tc := range []struct{ in, want string }{
		{"a\n\n\nb", "a\n\nb"},
		{"a\n\n\n\n\n\nb", "a\n\nb"},
		{"a\n\nb", "a\n\nb"},
		{"a\nb", "a\nb"},
	} {
		if got := collapseBlankLines(tc.in); got != tc.want {
			t.Errorf("collapseBlankLines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// And the rendered document is exactly the two paragraphs, with the one
	// blank line between them: a renderer that returned "" would satisfy
	// "contains no blank-line run" and nothing else.
	if got, want := render(t, "<div><div><p>one</p></div></div><div><p>two</p></div>"), "one\n\ntwo\n"; got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

// TestMarkdownDropsDangerousSchemes: readable.md is read by the dashboard,
// the chunker and whatever LLM gets the chunk. A URL scheme that is not a
// document is not carried into it.
func TestMarkdownDropsDangerousSchemes(t *testing.T) {
	got := render(t, `<a href="file:///etc/passwd">f</a> <a href="vbscript:evil()">v</a> `+
		`<a href="data:text/html,x">d</a> <img src="file:///etc/shadow" alt="i">`+
		`<a href="/ok">ok</a>`)
	for _, unwanted := range []string{"file://", "vbscript:", "data:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("markdown kept the %q scheme:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "[ok](https://example.com/ok)") {
		t.Errorf("an http link should still resolve:\n%s", got)
	}
}
