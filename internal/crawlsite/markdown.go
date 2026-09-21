package crawlsite

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// skippedTags never contribute text. Script and style are the important
// ones — they are also TRU-03's "strip scripts from archives", which the
// readable artifact gets for free by never including them.
var skippedTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"svg": true, "canvas": true, "iframe": true, "object": true,
	"embed": true, "form": true, "button": true, "input": true,
	"select": true, "textarea": true, "label": true, "audio": true,
	"video": true, "map": true, "area": true,
}

// mdWriter turns a parsed HTML subtree into Markdown. It is a small
// converter on purpose: headings, paragraphs, lists, quotes, code, links,
// images and tables cover what a readable article is made of, and anything
// it does not know still contributes its text rather than disappearing.
type mdWriter struct {
	b    strings.Builder
	base *url.URL
}

// renderMarkdown renders n's children as Markdown, resolving relative URLs
// against base.
func renderMarkdown(n *html.Node, base *url.URL) string {
	w := &mdWriter{base: base}
	w.blockChildren(n)
	return strings.TrimSpace(collapseBlankLines(w.b.String())) + "\n"
}

// collapseBlankLines reduces any run of blank lines to one, so a page built
// from deeply nested divs does not render as a column of whitespace.
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

func (w *mdWriter) para(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	w.b.WriteString(s)
	w.b.WriteString("\n\n")
}

func (w *mdWriter) blockChildren(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.block(c)
	}
}

func (w *mdWriter) block(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.para(squashSpace(n.Data))
		return
	case html.ElementNode:
	default:
		return
	}
	tag := n.Data
	if skippedTags[tag] {
		return
	}
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		w.para(strings.Repeat("#", int(tag[1]-'0')) + " " + w.inline(n))
	case "p", "figcaption", "dd", "dt", "summary":
		w.para(w.inline(n))
	case "hr":
		w.para("---")
	case "br":
		return
	case "ul", "ol":
		w.list(n, tag == "ol")
	case "blockquote":
		inner := &mdWriter{base: w.base}
		inner.blockChildren(n)
		var quoted []string
		for _, line := range strings.Split(strings.TrimSpace(inner.b.String()), "\n") {
			quoted = append(quoted, strings.TrimRight("> "+line, " "))
		}
		w.para(strings.Join(quoted, "\n"))
	case "pre":
		w.para("```\n" + strings.Trim(textOf(n), "\n") + "\n```")
	case "table":
		w.table(n)
	case "img":
		w.para(w.image(n))
	default:
		// A container: recurse if it holds block-level children, otherwise
		// treat the whole thing as one paragraph of inline content.
		if hasBlockChild(n) {
			w.blockChildren(n)
			return
		}
		w.para(w.inlineOne(n))
	}
}

// blockTags decide whether a container is recursed into or flattened into
// one paragraph.
var blockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"div": true, "dl": true, "fieldset": true, "figure": true,
	"figcaption": true, "footer": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "header": true, "hr": true,
	"li": true, "main": true, "nav": true, "ol": true, "p": true,
	"pre": true, "section": true, "table": true, "ul": true, "details": true,
}

func hasBlockChild(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && blockTags[c.Data] {
			return true
		}
	}
	return false
}

func (w *mdWriter) list(n *html.Node, ordered bool) {
	idx := 0
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "li" {
			continue
		}
		idx++
		marker := "- "
		if ordered {
			marker = itoa(idx) + ". "
		}
		inner := &mdWriter{base: w.base}
		if hasBlockChild(c) {
			inner.blockChildren(c)
		} else {
			inner.para(inner.inline(c))
		}
		lines := strings.Split(strings.TrimSpace(inner.b.String()), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		b.WriteString(marker + lines[0] + "\n")
		// Continuation lines — a nested list, a second paragraph — are
		// indented under the marker; a nested list rendered itself with no
		// indent of its own, so each level of nesting adds exactly one.
		for _, line := range lines[1:] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			b.WriteString("  " + line + "\n")
		}
	}
	if b.Len() > 0 {
		w.b.WriteString(b.String())
		w.b.WriteString("\n")
	}
}

func (w *mdWriter) table(n *html.Node) {
	rows := collectRows(n)
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	var b strings.Builder
	for i, r := range rows {
		cells := make([]string, width)
		for j := range cells {
			if j < len(r) {
				cells[j] = strings.ReplaceAll(r[j], "|", "\\|")
			}
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		if i == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
		}
	}
	w.para(strings.TrimRight(b.String(), "\n"))
}

// collectRows flattens thead/tbody/tfoot away; a Markdown table has no
// place for them and the first row is the header either way.
func collectRows(n *html.Node) [][]string {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "thead", "tbody", "tfoot":
				walk(c)
			case "tr":
				var cells []string
				for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
						cells = append(cells, strings.TrimSpace(squashSpace(textOf(cell))))
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return rows
}

// inline renders n's children as one line of Markdown.
func (w *mdWriter) inline(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(w.inlineOne(c))
	}
	return strings.TrimSpace(squashSpace(b.String()))
}

// inlineOne renders a single node as inline Markdown. block() calls it for
// an element with no block-level children, so a bare <a> in a container
// still renders as a link and not as its bare text.
func (w *mdWriter) inlineOne(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return squashSpace(n.Data)
	case html.ElementNode:
	default:
		return ""
	}
	if skippedTags[n.Data] {
		return ""
	}
	switch n.Data {
	case "br":
		return "  \n"
	case "strong", "b":
		return wrapNonEmpty(w.inline(n), "**")
	case "em", "i", "cite":
		return wrapNonEmpty(w.inline(n), "*")
	case "code", "kbd", "samp":
		return wrapNonEmpty(strings.TrimSpace(squashSpace(textOf(n))), "`")
	case "del", "s":
		return wrapNonEmpty(w.inline(n), "~~")
	case "a":
		return w.link(n)
	case "img":
		return w.image(n)
	default:
		return w.inline(n)
	}
}

func (w *mdWriter) link(n *html.Node) string {
	text := w.inline(n)
	href := w.resolve(attr(n, "href"))
	if href == "" || text == "" {
		return text
	}
	return "[" + text + "](" + href + ")"
}

func (w *mdWriter) image(n *html.Node) string {
	src := attr(n, "src")
	if src == "" {
		// Lazy-loaded images keep the real URL out of src until they scroll
		// into view; without a browser, these attributes are all there is.
		for _, k := range []string{"data-src", "data-original", "data-lazy-src"} {
			if v := attr(n, k); v != "" {
				src = v
				break
			}
		}
	}
	src = w.resolve(src)
	if src == "" {
		return ""
	}
	return "![" + squashSpace(attr(n, "alt")) + "](" + src + ")"
}

// resolve makes a document-relative URL absolute, and drops the ones that
// are not addresses at all.
func (w *mdWriter) resolve(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return ""
	}
	switch {
	case strings.HasPrefix(strings.ToLower(raw), "javascript:"),
		strings.HasPrefix(strings.ToLower(raw), "data:"):
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if w.base != nil {
		u = w.base.ResolveReference(u)
	}
	return u.String()
}

func wrapNonEmpty(s, with string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return with + s + with
}

// textOf returns an element's text with markup removed, skipping the tags
// that never contribute text.
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur.Type == html.TextNode {
			b.WriteString(cur.Data)
			return
		}
		if cur.Type == html.ElementNode && skippedTags[cur.Data] {
			return
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// squashSpace collapses every run of whitespace to one space. HTML says
// they are all the same; Markdown does not, and a newline left in the
// middle of a paragraph becomes a line break in some renderers.
func squashSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v', ' ':
			space = true
		default:
			if space {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// itoa avoids pulling strconv in for one call site in a hot loop.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
