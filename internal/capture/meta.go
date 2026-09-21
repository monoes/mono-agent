// Package capture models the browser-capture envelope: the provenance
// record and set of artifacts that every capture — from the Chrome
// extension, from monobrowse, or from a crawl — lands in
// ~/.monomind/inbox/<ts>-<slug>/ as. See docs/BROWSER_TRACK_PLAN.md
// ("Contract: the capture envelope").
//
// The package deliberately knows nothing about WebSockets or the extension:
// it takes already-decoded JSON objects (the `data` field of a page_capture
// response), reassembles chunked artifacts, and writes the envelope to
// disk. internal/extension owns the transport.
package capture

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// Meta is the provenance record written as meta.json. Field types match the
// envelope schema in the plan; nullable fields are pointers so a JSON null
// round-trips as null rather than silently becoming "".
//
// Decoding is deliberately lenient (see UnmarshalJSON): the JS half of the
// capture is expected to grow fields faster than this struct does, and a
// capture is far too expensive to throw away over one field whose type
// drifted. Anything this struct cannot take — an unknown field, or a known
// one whose value will not fit — is kept verbatim in Extra and written back
// out, so a newer extension's metadata is never silently dropped on the
// floor by an older binary.
type Meta struct {
	URL          string     `json:"url"`
	CanonicalURL string     `json:"canonicalUrl"`
	Title        string     `json:"title"`
	Byline       *string    `json:"byline"`
	PublishedAt  *string    `json:"publishedAt"`
	CapturedAt   string     `json:"capturedAt"`
	HTTPStatus   int        `json:"httpStatus"`
	ContentHash  string     `json:"contentHash"`
	Favicon      string     `json:"favicon"`
	Selection    *Selection `json:"selection"`
	Note         *string    `json:"note"`
	Tags         []string   `json:"tags"`
	Collection   *string    `json:"collection"`
	Source       string     `json:"source"`

	// Extra carries everything the sender set that this struct has no home
	// for, verbatim, so MarshalJSON can put it back into meta.json.
	Extra map[string]json.RawMessage `json:"-"`
}

// Selection is the captured range for a selection capture (CLIP-04). The
// text alone would not be enough: the range — which element, and where
// inside it — is what lets a highlight be put back on the page later, so
// this is an object and not a string.
type Selection struct {
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
	Path        string `json:"path"`
	StartOffset int    `json:"startOffset"`
	EndOffset   int    `json:"endOffset"`

	// Extra preserves selection fields this struct does not know, on the
	// same terms as Meta.Extra.
	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes leniently; see Meta.UnmarshalJSON.
func (s *Selection) UnmarshalJSON(b []byte) error {
	extra, err := lenientDecode(b, s)
	if err != nil {
		return err
	}
	s.Extra = extra
	return nil
}

// MarshalJSON writes the known fields plus anything preserved in Extra.
func (s Selection) MarshalJSON() ([]byte, error) { return marshalWithExtra(s, s.Extra) }

// The meta.source values, one per producer of these envelopes. They live
// together here rather than in each producer because a consumer reading an
// inbox has to recognise all three, and a private copy in one package is
// how the three quietly drift apart.
const (
	// SourceExtension is a page saved from the user's real, logged-in
	// Chrome via the extension bridge.
	SourceExtension = "extension"
	// SourceCrawl is a page fetched by the crawler — no browser, so no
	// MHTML: its byte-fidelity artifact is ArtifactHTML.
	SourceCrawl = "crawl"
	// SourceMonobrowse is a page captured through headless monobrowse.
	SourceMonobrowse = "monobrowse"
)

// UnmarshalJSON decodes field by field instead of in one shot, so a single
// field of the wrong type (a JS `httpStatus: "200"`, say) costs that one
// field rather than the whole capture. Only a payload that is not a JSON
// object at all is an error.
func (m *Meta) UnmarshalJSON(b []byte) error {
	extra, err := lenientDecode(b, m)
	if err != nil {
		return err
	}
	m.Extra = extra
	return nil
}

// MarshalJSON writes the known fields plus anything preserved in Extra.
func (m Meta) MarshalJSON() ([]byte, error) { return marshalWithExtra(m, m.Extra) }

// lenientDecode decodes a JSON object into the struct target points at, one
// field at a time, and returns everything the struct could not take: keys
// it does not know, and keys whose value will not fit the field's Go type.
// Both are handed back for the caller to preserve verbatim.
//
// Preserving the second kind is the point. Dropping it is how a capture
// ends up with `"selection": null` in meta.json because the JS side started
// sending the selection's range as an object while this struct still said
// string — the data is gone, and nothing anywhere reported a problem. A
// field that survives in a shape Go cannot read is strictly better than a
// field that quietly became null.
func lenientDecode(b []byte, target any) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	v := reflect.ValueOf(target).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		key := jsonKey(t.Field(i))
		if key == "" {
			continue
		}
		val, ok := raw[key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(val, v.Field(i).Addr().Interface()); err != nil {
			// A partial decode can leave the field half-populated; reset
			// it so a Go caller sees a clean zero, and leave the key in
			// raw so the sender's own value still reaches meta.json.
			v.Field(i).SetZero()
			continue
		}
		delete(raw, key)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return raw, nil
}

// marshalWithExtra renders a struct's known fields, then overlays the
// preserved ones. Extra wins on a collision: it only ever holds what the
// struct could not take, so the sender's original value is more faithful
// than the zero value left in the field.
func marshalWithExtra(v any, extra map[string]json.RawMessage) ([]byte, error) {
	rv := reflect.ValueOf(v)
	rt := rv.Type()
	out := make(map[string]json.RawMessage, rt.NumField()+len(extra))
	for i := 0; i < rt.NumField(); i++ {
		key := jsonKey(rt.Field(i))
		if key == "" {
			continue
		}
		b, err := json.Marshal(rv.Field(i).Interface())
		if err != nil {
			return nil, err
		}
		out[key] = b
	}
	for k, raw := range extra {
		out[k] = raw
	}
	return json.Marshal(out)
}

// jsonKey returns the JSON object key a struct field maps to, or "" for a
// field that is not serialized (`json:"-"`).
func jsonKey(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	switch name {
	case "-":
		return ""
	case "":
		return f.Name
	default:
		return name
	}
}

// Normalize fills in the fields the writer needs but the sender may have
// left out. It never overwrites a value the sender did set.
func (m *Meta) Normalize(now time.Time) {
	if strings.TrimSpace(m.CapturedAt) == "" {
		m.CapturedAt = now.UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(m.Source) == "" {
		m.Source = SourceExtension
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
}

// DedupeURL is the URL half of the dedupe key (canonicalUrl + contentHash):
// the canonical URL when the page declared one, the visited URL otherwise.
func (m *Meta) DedupeURL() string {
	if s := strings.TrimSpace(m.CanonicalURL); s != "" {
		return s
	}
	return strings.TrimSpace(m.URL)
}

// capturedTime parses CapturedAt, falling back to fallback for an empty or
// unparseable value so a bad timestamp can never stop a capture landing.
func (m *Meta) capturedTime(fallback time.Time) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, m.CapturedAt); err == nil {
			return t
		}
	}
	return fallback
}
