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
		got := render(t, tc.in)
		if !strings.Contains(got, tc.want) {
			t.Errorf("render(%q) =\n%q\nwant it to contain\n%q", tc.in, got, tc.want)
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
	got := render(t, "<div><div><div><p>one</p></div></div></div><div><p>two</p></div>")
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("nested containers produced blank-line runs:\n%q", got)
	}
}
