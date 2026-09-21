package crawlsite

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// envelope maps one fetched page onto the capture envelope contract. The
// artifacts are the bytes the server sent and the Markdown that gets
// chunked; everything else is provenance.
func (c *Crawler) envelope(f *fetched, ex *extracted, it item, canonical string) *capture.Envelope {
	meta := capture.Meta{
		URL:          f.Normalized,
		CanonicalURL: canonical,
		Title:        ex.Title,
		CapturedAt:   c.opts.Now().UTC().Format(time.RFC3339),
		HTTPStatus:   f.Status,
		Favicon:      ex.Favicon,
		Tags:         append([]string{}, c.opts.Tags...),
		Source:       capture.SourceCrawl,
	}
	if ex.Byline != "" {
		byline := ex.Byline
		meta.Byline = &byline
	}
	if ex.PublishedAt != "" {
		published := ex.PublishedAt
		meta.PublishedAt = &published
	}
	if c.opts.Collection != "" {
		collection := c.opts.Collection
		meta.Collection = &collection
	}
	meta.Extra = crawlExtra(f, ex, it)

	env := &capture.Envelope{
		Meta: meta,
		Artifacts: map[string]capture.Artifact{
			capture.ArtifactHTML:     capture.Inline(f.Body),
			capture.ArtifactReadable: capture.Inline([]byte(ex.Markdown)),
		},
	}
	if f.Truncated {
		env.Warnings = append(env.Warnings,
			fmt.Sprintf("%s: page exceeded the %d byte limit and was truncated", f.Normalized, c.opts.MaxBytes))
	}
	return env
}

// crawlExtra is the provenance a crawl knows and the envelope schema has no
// field for. It rides in meta.json verbatim rather than being dropped: how
// a document was reached is part of how much it should be trusted.
func crawlExtra(f *fetched, ex *extracted, it item) map[string]json.RawMessage {
	extra := map[string]json.RawMessage{}
	put := func(key string, value any) {
		if b, err := json.Marshal(value); err == nil {
			extra[key] = b
		}
	}
	if ex.Description != "" {
		put("description", ex.Description)
	}
	if ex.Lang != "" {
		put("lang", ex.Lang)
	}
	if ex.SiteName != "" {
		put("siteName", ex.SiteName)
	}
	put("crawl", map[string]any{
		"seed":        it.seed,
		"depth":       it.depth,
		"referrer":    it.referrer,
		"contentType": f.ContentType,
		"htmlBytes":   len(f.Body),
		"truncated":   f.Truncated,
	})
	return extra
}
