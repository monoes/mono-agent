package crawlsite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// DefaultUserAgent identifies the crawler honestly. A site that wants to
// keep this crawler out can name it in robots.txt, which only works if it
// is told what to name.
const DefaultUserAgent = "monoagent-crawl/1 (+https://github.com/monoes/mono-agent)"

// DefaultMaxBytes bounds one page's HTML. Eight megabytes of markup is
// already far past anything worth reading, and an unbounded read is how a
// crawl of a misbehaving endpoint becomes an out-of-memory kill (TRU-04).
const DefaultMaxBytes int64 = 8 << 20

// errNotHTML marks a response that was fetched successfully and is not a
// document this crawl can read. It is a skip, not a failure.
var errNotHTML = errors.New("not an HTML document")

// htmlTypes are the media types worth parsing. Everything else — a PDF, an
// image, a zip — is skipped: the capture envelope for a PDF is a different
// story (the extension's), and guessing here produces garbage Markdown.
var htmlTypes = map[string]bool{
	"text/html":             true,
	"application/xhtml+xml": true,
	"application/xml+html":  true,
}

// binaryExtensions are skipped without a request at all. Following every
// link to a 200MB video to discover its Content-Type is neither polite nor
// fast.
var binaryExtensions = map[string]bool{
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true, ".bz2": true,
	".xz": true, ".7z": true, ".rar": true, ".exe": true, ".dmg": true,
	".pkg": true, ".deb": true, ".rpm": true, ".iso": true, ".jpg": true,
	".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true,
	".svg": true, ".ico": true, ".bmp": true, ".tiff": true, ".mp3": true,
	".mp4": true, ".m4a": true, ".m4v": true, ".mov": true, ".avi": true,
	".mkv": true, ".webm": true, ".wav": true, ".flac": true, ".ogg": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".css": true, ".js": true, ".mjs": true, ".json": true, ".xml": true,
	".rss": true, ".atom": true, ".csv": true, ".doc": true, ".docx": true,
	".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true, ".epub": true,
}

// looksBinary reports whether a URL's path ends in an extension that is
// certainly not a web page.
func looksBinary(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return binaryExtensions[strings.ToLower(path.Ext(u.Path))]
}

// fetched is one HTTP response the crawl kept.
type fetched struct {
	URL         *url.URL // the final URL, after redirects
	Normalized  string
	Status      int
	ContentType string
	Body        []byte
	Truncated   bool
}

// fetch GETs one page. A non-HTML response is closed without reading its
// body, so a mislabelled 2GB download costs one round trip and not a disk.
func (c *Crawler) fetch(ctx context.Context, rawURL string) (*fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	req.Header.Set("Accept-Language", "en;q=0.9,*;q=0.5")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("http %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	ctype := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(ctype)
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" {
		// No Content-Type at all: sniff the first bytes rather than guess.
		mediaType = "text/html"
	}
	if !htmlTypes[mediaType] {
		return nil, fmt.Errorf("%w (%s)", errNotHTML, mediaType)
	}

	limit := c.opts.MaxBytes
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	truncated := int64(len(body)) > limit
	if truncated {
		body = body[:limit]
	}

	final := resp.Request.URL
	normalized, err := normalizeParsed(final)
	if err != nil {
		return nil, err
	}
	return &fetched{
		URL:         final,
		Normalized:  normalized,
		Status:      resp.StatusCode,
		ContentType: ctype,
		Body:        body,
		Truncated:   truncated,
	}, nil
}
