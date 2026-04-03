# AI-Assisted Web Crawling Nodes — Design Spec

**Date:** 2026-04-03  
**Status:** Approved  
**Scope:** Two new workflow nodes (`ai.read_page`, `ai.extract_page`) for intelligent web content extraction.

---

## Problem

AI workflows that process web content waste tokens on HTML boilerplate — scripts, styles, nav menus, ads, tracking pixels. A 50KB webpage might have 2KB of actual content. Users need a node that returns only what a human would see, in a format optimized for LLM consumption.

## Solution

Two focused nodes sharing a common content engine:

- **`ai.read_page`** — "Give me what's on this page" — returns clean markdown + structured sections
- **`ai.extract_page`** — "Pull these specific fields" — AI-assisted or manual CSS extraction

Both handle their own fetching (static HTTP or headless browser) with auto-detection.

---

## Architecture

### Shared Engine (`internal/nodes/ai/crawl/engine.go`)

Three pipeline stages:

#### 1. Fetch — `FetchPage(ctx, url, options) → (rawHTML, finalURL, error)`

| Mode | Mechanism | Use Case |
|------|-----------|----------|
| `static` | `http.Get` with 30s timeout, follows redirects | Articles, blogs, docs, APIs |
| `browser` | Rod headless, waits for network idle, captures rendered DOM | SPAs, React/Vue sites, pages requiring JS |
| `auto` (default) | Static first → if `<body>` visible text < 200 chars, retry with browser | Best of both — fast when possible, correct when needed |

Options:
- `wait_selector` — CSS selector to wait for before extracting (browser mode only)
- `timeout` — fetch timeout (default 30s static, 60s browser)

#### 2. Clean — `CleanContent(rawHTML) → PageContent`

Uses goquery to parse DOM and strip non-visible content:

**Removed:**
- `<script>`, `<style>`, `<noscript>`, `<svg>`, `<iframe>`
- Hidden elements: `display:none`, `visibility:hidden`, `aria-hidden="true"`
- Comment nodes

**Optionally removed** (configurable via `keep_nav`):
- `<nav>`, `<header>`, `<footer>` — usually boilerplate navigation/legal text

**Extracted into `PageContent` struct:**

```go
type PageContent struct {
    Title       string     // <title> or first <h1>
    Description string     // meta[name=description]
    Author      string     // meta[name=author] or article:author
    PublishedAt string     // article:published_time or <time> element
    URL         string     // canonical URL or final redirected URL
    Favicon     string     // link[rel=icon] href
    MainText    string     // all visible text, paragraphs preserved
    Markdown    string     // full cleaned page as markdown
    Links       []Link     // {Text, URL, IsExternal}
    Images      []Image    // {Alt, Src, Width, Height}
    Headings    []Heading  // {Level, Text}
    Tables      [][]string // table data as 2D string arrays
    TokenCount  int        // approximate token count of Markdown field
}

type Link struct {
    Text       string `json:"text"`
    URL        string `json:"url"`
    IsExternal bool   `json:"external"`
}

type Image struct {
    Alt    string `json:"alt"`
    Src    string `json:"src"`
    Width  int    `json:"width,omitempty"`
    Height int    `json:"height,omitempty"`
}

type Heading struct {
    Level int    `json:"level"`
    Text  string `json:"text"`
}
```

#### 3. ToMarkdown — DOM tree to clean markdown

Conversion rules:
- `<h1>`–`<h6>` → `#`–`######`
- `<p>` → paragraph with blank line separator
- `<a href>` → `[text](url)`
- `<img>` → `![alt](src)`
- `<ul>/<ol>` → `- ` / `1. ` with nesting
- `<table>` → markdown pipe table with header separator
- `<pre>/<code>` → fenced code blocks with language hint if available
- `<blockquote>` → `> ` prefix
- `<strong>/<b>` → `**text**`
- `<em>/<i>` → `*text*`
- Whitespace collapsed, consecutive empty lines reduced to one

Token count is estimated at ~0.75 tokens per word (approximation sufficient for truncation decisions).

---

## Node 1: `ai.read_page`

### Schema

```json
{
  "credential_platform": null,
  "fields": [
    {"key": "url", "label": "URL", "type": "text", "required": true, "placeholder": "https://example.com/article"},
    {"key": "render_mode", "label": "Render Mode", "type": "select", "required": false, "default": "auto", "options": ["auto", "static", "browser"]},
    {"key": "include_links", "label": "Include Links", "type": "boolean", "default": true},
    {"key": "include_images", "label": "Include Images", "type": "boolean", "default": true},
    {"key": "include_tables", "label": "Include Tables", "type": "boolean", "default": true},
    {"key": "keep_nav", "label": "Keep Nav/Header/Footer", "type": "boolean", "default": false},
    {"key": "max_tokens", "label": "Max Tokens", "type": "number", "default": 0, "help": "Truncate markdown to ~N tokens. 0 = no limit."},
    {"key": "wait_selector", "label": "Wait For Selector", "type": "text", "placeholder": "#main-content", "depends_on": {"key": "render_mode", "values": ["browser"]}, "help": "CSS selector to wait for before extracting (browser mode)."}
  ]
}
```

### Execution Flow

1. Read `url` from config (or `item.url` from upstream if config url is empty)
2. Call `FetchPage(ctx, url, {RenderMode, WaitSelector})`
3. Call `CleanContent(rawHTML, {KeepNav})`
4. Optionally truncate `Markdown` to `max_tokens`
5. Build output item, omitting `links`/`images`/`tables` if disabled
6. Return on `"main"` handle; on fetch error return on `"error"` handle

### Output Item

```json
{
  "url": "https://example.com/article",
  "title": "The Article Title",
  "description": "Meta description text",
  "author": "Jane Doe",
  "published_at": "2026-03-15",
  "markdown": "# The Article Title\n\nFirst paragraph of content...",
  "main_text": "The Article Title. First paragraph of content...",
  "links": [{"text": "Related Post", "url": "https://example.com/other", "external": false}],
  "images": [{"alt": "Hero image", "src": "https://example.com/hero.jpg", "width": 1200, "height": 630}],
  "headings": [{"level": 1, "text": "The Article Title"}, {"level": 2, "text": "Introduction"}],
  "tables": [["Col A", "Col B"], ["val1", "val2"]],
  "token_count": 1250,
  "render_mode_used": "static",
  "fetch_time_ms": 340
}
```

### CLI

```bash
# Basic
monoes node run ai.read_page --config '{"url":"https://example.com/article"}'

# Browser mode with token limit
monoes node run ai.read_page --config '{"url":"https://spa.com","render_mode":"browser","max_tokens":4000}'

# Pipeline: read page → feed to AI chat
monoes node run ai.read_page --config '{"url":"https://docs.example.com"}' | monoes node run ai.chat --config '{"prompt":"Summarize this page"}'
```

---

## Node 2: `ai.extract_page`

### Schema

```json
{
  "credential_platform": null,
  "fields": [
    {"key": "url", "label": "URL", "type": "text", "required": true, "placeholder": "https://shop.com/product/123"},
    {"key": "extract_mode", "label": "Extraction Mode", "type": "select", "required": true, "default": "natural", "options": ["natural", "css"]},
    {"key": "prompt", "label": "What to Extract", "type": "textarea", "required": false, "rows": 3, "placeholder": "Extract product name, price, rating, and all reviews", "depends_on": {"key": "extract_mode", "values": ["natural"]}, "help": "Describe what you want to extract in plain English."},
    {"key": "fields", "label": "CSS Selectors (JSON)", "type": "code", "language": "json", "required": false, "rows": 5, "placeholder": "{\"name\": \"h1.title\", \"price\": \".price-tag\"}", "depends_on": {"key": "extract_mode", "values": ["css"]}},
    {"key": "list_selector", "label": "List Selector", "type": "text", "placeholder": ".product-card", "help": "If set, extracts an array of items matching this selector. Each item gets the fields extracted."},
    {"key": "render_mode", "label": "Render Mode", "type": "select", "default": "auto", "options": ["auto", "static", "browser"]},
    {"key": "wait_selector", "label": "Wait For Selector", "type": "text", "depends_on": {"key": "render_mode", "values": ["browser"]}}
  ]
}
```

### Execution Flow — Natural Mode

1. Fetch + clean the page (same engine)
2. Truncate cleaned HTML to 500KB
3. POST to monoes_apis `/generate-config`:
   - `configName`: auto-generated from URL domain + prompt hash
   - `htmlContent`: cleaned HTML
   - `purpose`: user's prompt
   - `extractionSchema`: auto-generated JSON schema from prompt keywords
4. Receive CSS selectors for each requested field
5. Apply selectors via goquery
6. If `list_selector` is set, iterate over matches and extract fields from each
7. Return extracted data + `selectors_used` (so users can switch to CSS mode)

### Execution Flow — CSS Mode

1. Fetch + clean the page
2. Parse `fields` JSON as `map[string]string` (field name → CSS selector)
3. Apply each selector via goquery, extract text content
4. If `list_selector` is set, iterate over matches
5. Return extracted data

### Output — Single Item

```json
{
  "url": "https://shop.com/product/123",
  "extracted": {
    "name": "Wireless Headphones",
    "price": "$79.99",
    "rating": "4.5 out of 5"
  },
  "selectors_used": {
    "name": "h1.product-title",
    "price": ".price-current",
    "rating": ".rating-value"
  },
  "extract_mode": "natural",
  "fetch_time_ms": 1200
}
```

### Output — List (with `list_selector`)

```json
{
  "url": "https://shop.com/product/123/reviews",
  "extracted": [
    {"author": "Jane", "text": "Great sound quality", "rating": "5"},
    {"author": "Bob", "text": "Good value", "rating": "4"}
  ],
  "count": 2,
  "selectors_used": {
    "author": ".reviewer-name",
    "text": ".review-body",
    "rating": ".stars"
  },
  "list_selector": ".review-card",
  "extract_mode": "natural"
}
```

### CLI

```bash
# Natural language extraction
monoes node run ai.extract_page \
  --config '{"url":"https://shop.com/p/123","extract_mode":"natural","prompt":"extract product name, price, and rating"}'

# CSS selectors (power user / repeat runs)
monoes node run ai.extract_page \
  --config '{"url":"https://shop.com/p/123","extract_mode":"css","fields":"{\"name\":\"h1\",\"price\":\".price\"}"}'

# List extraction
monoes node run ai.extract_page \
  --config '{"url":"https://shop.com/products","extract_mode":"natural","prompt":"extract name and price","list_selector":".product-card"}'
```

---

## File Structure

```
internal/nodes/ai/crawl/
├── engine.go            # FetchPage, CleanContent, ToMarkdown
├── read_page.go         # ai.read_page NodeExecutor
├── extract_page.go      # ai.extract_page NodeExecutor
└── register.go          # RegisterAll

internal/workflow/schemas/
├── ai.read_page.json    # UI schema
└── ai.extract_page.json # UI schema
```

### Registration

`register.go` exports `RegisterAll(registry)` registering both nodes. Called from `cmd/monoes/node.go:buildNodeRegistry`.

Both nodes added to `GetWorkflowNodeTypes` in `wails-app/app.go` under the `ai` category.

---

## Dependencies

No new Go modules. Uses existing:
- `github.com/PuerkitoBio/goquery` — DOM parsing
- `github.com/go-rod/rod` + `github.com/go-rod/stealth` — headless browser
- `net/http` — static fetch
- `internal/config.APIClient` — monoes_apis calls

---

## Error Handling

| Error | Behavior |
|-------|----------|
| Fetch failure (DNS, timeout, 4xx/5xx) | Return on `"error"` output handle with `{url, error, status_code}` |
| Browser launch failure | Fall back to static mode, add `warning` to output |
| monoes_apis unreachable (extract natural mode) | Return error with message; include cleaned markdown in output so workflow isn't fully blocked |
| Invalid CSS selector | Return error per-field: `{field: "price", error: "invalid selector"}` |
| Empty page / no content | Return empty `markdown` + `main_text` with `warning: "page appears empty"` |

---

## Future (v2, not in this implementation)

- **Multi-page crawling** — follow links with depth limit and domain filter
- **Caching** — cache fetched pages by URL+timestamp to avoid re-fetching in loops
- **Selector learning** — save successful AI-generated selectors and reuse on same domain
- **Screenshot** — capture a viewport screenshot alongside content (Rod supports this)

---

## Token Efficiency Comparison

Example: crawling a typical news article page (CNN, BBC, Medium):

| Method | Tokens | Content Quality |
|--------|--------|----------------|
| Raw HTML | ~15,000 | Mostly noise (scripts, ads, nav) |
| `data.html` text strip | ~4,000 | All text including menus, footers, ads |
| `ai.read_page` markdown | ~1,500 | Clean article content only |
| `ai.read_page` main_text | ~1,200 | Plain text, no formatting |

**10x token reduction** compared to raw HTML, **3x** compared to naive text stripping.
