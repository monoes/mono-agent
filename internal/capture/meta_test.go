package capture

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMetaDecodesPlanSchema(t *testing.T) {
	const raw = `{
	  "url": "https://example.com/a", "canonicalUrl": "https://example.com/a",
	  "title": "A", "byline": null, "publishedAt": null,
	  "capturedAt": "2026-09-21T10:11:12Z", "httpStatus": 200,
	  "contentHash": "sha256:abc", "favicon": "https://example.com/f.ico",
	  "selection": null, "note": null, "tags": [], "collection": null,
	  "source": "extension"
	}`
	var m Meta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.URL != "https://example.com/a" || m.HTTPStatus != 200 || m.Source != "extension" {
		t.Fatalf("decoded = %+v", m)
	}
	if m.Byline != nil || m.Note != nil || m.Collection != nil {
		t.Fatalf("nullable fields should stay nil: %+v", m)
	}
	if m.Tags == nil || len(m.Tags) != 0 {
		t.Fatalf("tags = %v, want an empty slice", m.Tags)
	}
}

// TestMetaKeepsUnknownFields is the forward-compatibility guarantee: the JS
// half will add fields before this struct learns about them, and they must
// survive into meta.json rather than being dropped by an older binary.
func TestMetaKeepsUnknownFields(t *testing.T) {
	const raw = `{"url":"https://example.com/a","readingTimeMinutes":7,
	              "highlights":[{"text":"x"}],"source":"extension"}`
	var m Meta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(m.Extra) != 2 {
		t.Fatalf("Extra = %v, want readingTimeMinutes and highlights", m.Extra)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if round["readingTimeMinutes"] != float64(7) {
		t.Fatalf("readingTimeMinutes lost: %v", round)
	}
	if _, ok := round["highlights"]; !ok {
		t.Fatalf("highlights lost: %v", round)
	}
	if round["url"] != "https://example.com/a" {
		t.Fatalf("url = %v", round["url"])
	}
}

// TestMetaToleratesWrongFieldTypes: one field the JS side typed differently
// must cost that field, not the whole capture — and the value must still
// reach meta.json rather than being quietly replaced with null.
func TestMetaToleratesWrongFieldTypes(t *testing.T) {
	const raw = `{"url":"https://example.com/a","httpStatus":"200",
	              "tags":"one,two","title":"Still here"}`
	var m Meta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal should tolerate a bad field type, got %v", err)
	}
	if m.URL != "https://example.com/a" || m.Title != "Still here" {
		t.Fatalf("good fields lost: %+v", m)
	}
	if m.HTTPStatus != 0 {
		t.Fatalf("httpStatus = %d, want the zero value", m.HTTPStatus)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if round["httpStatus"] != "200" {
		t.Fatalf("httpStatus = %v, want the sender's own \"200\" preserved, not a silently zeroed field", round["httpStatus"])
	}
	if round["tags"] != "one,two" {
		t.Fatalf("tags = %v, want the sender's value preserved", round["tags"])
	}
}

// TestMetaSelectionIsARange is the regression test for the contract
// mismatch that made this whole rule necessary: the extension sends the
// selection as an object (CLIP-04 wants the range, not just the text), and
// a *string field would have decoded it to nil and written "selection":
// null with no error anywhere.
func TestMetaSelectionIsARange(t *testing.T) {
	const raw = `{"url":"https://example.com/a","selection":{"text":"the quoted sentence",
	  "truncated":false,"path":"article > p:nth-of-type(3)","startOffset":12,"endOffset":204}}`
	var m Meta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Selection == nil {
		t.Fatal("selection decoded to nil — the range was dropped")
	}
	if m.Selection.Text != "the quoted sentence" {
		t.Fatalf("selection.text = %q", m.Selection.Text)
	}
	if m.Selection.Path != "article > p:nth-of-type(3)" {
		t.Fatalf("selection.path = %q", m.Selection.Path)
	}
	if m.Selection.StartOffset != 12 || m.Selection.EndOffset != 204 {
		t.Fatalf("selection range = %d..%d, want 12..204", m.Selection.StartOffset, m.Selection.EndOffset)
	}
	if m.Selection.Truncated {
		t.Fatal("selection.truncated = true, want false")
	}

	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var round struct {
		Selection map[string]any `json:"selection"`
	}
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if round.Selection == nil {
		t.Fatalf("selection written as null:\n%s", out)
	}
	if round.Selection["path"] != "article > p:nth-of-type(3)" || round.Selection["endOffset"] != float64(204) {
		t.Fatalf("selection round-tripped as %v", round.Selection)
	}
}

func TestMetaSelectionAbsentStaysNull(t *testing.T) {
	var m Meta
	if err := json.Unmarshal([]byte(`{"url":"u","selection":null}`), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Selection != nil {
		t.Fatalf("selection = %+v, want nil for a whole-page capture", m.Selection)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), `"selection":null`) {
		t.Fatalf("selection not written as null:\n%s", out)
	}
}

// TestMetaSelectionKeepsUnknownShape: whatever the selection turns into
// next, it must survive the trip rather than vanish.
func TestMetaSelectionKeepsUnknownShape(t *testing.T) {
	var m Meta
	if err := json.Unmarshal([]byte(`{"url":"u","selection":"just the text"}`), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Selection != nil {
		t.Fatalf("selection = %+v, want nil for a shape this struct cannot take", m.Selection)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if round["selection"] != "just the text" {
		t.Fatalf("selection = %v, want the sender's value preserved", round["selection"])
	}
}

func TestSelectionKeepsUnknownFields(t *testing.T) {
	var s Selection
	if err := json.Unmarshal([]byte(`{"text":"x","containerId":"main","rects":[[1,2]]}`), &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(s.Extra) != 2 {
		t.Fatalf("Extra = %v", s.Extra)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if round["containerId"] != "main" || round["rects"] == nil {
		t.Fatalf("selection extras lost: %v", round)
	}
}

// TestMetaKeepsTheExtensionsExtraFields pins the fields the JS side sends
// today that this struct has no column for.
func TestMetaKeepsTheExtensionsExtraFields(t *testing.T) {
	const raw = `{"url":"https://example.com/a","siteName":"Example","lang":"en",
	  "excerpt":"A short summary.","wordCount":1840,
	  "preparation":{"scrolled":true,"lazyImages":12,"durationMs":900}}`
	var m Meta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	for key, want := range map[string]any{
		"siteName":  "Example",
		"lang":      "en",
		"excerpt":   "A short summary.",
		"wordCount": float64(1840),
	} {
		if round[key] != want {
			t.Errorf("meta.json %s = %v, want %v", key, round[key], want)
		}
	}
	prep, ok := round["preparation"].(map[string]any)
	if !ok || prep["lazyImages"] != float64(12) {
		t.Fatalf("preparation = %v", round["preparation"])
	}
}

func TestMetaToleratesMissingFields(t *testing.T) {
	var m Meta
	if err := json.Unmarshal([]byte(`{"url":"https://example.com/a"}`), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.URL != "https://example.com/a" {
		t.Fatalf("url = %q", m.URL)
	}
	if m.CapturedAt != "" || m.Tags != nil {
		t.Fatalf("absent fields invented values: %+v", m)
	}
}

func TestMetaRejectsNonObject(t *testing.T) {
	var m Meta
	if err := json.Unmarshal([]byte(`"just a string"`), &m); err == nil {
		t.Fatal("accepted a non-object meta")
	}
}

func TestMetaNormalize(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 11, 12, 0, time.UTC)
	m := Meta{URL: "https://example.com/a"}
	m.Normalize(now)
	if m.CapturedAt != "2026-09-21T10:11:12Z" {
		t.Fatalf("capturedAt = %q", m.CapturedAt)
	}
	if m.Source != SourceExtension {
		t.Fatalf("source = %q", m.Source)
	}
	if m.Tags == nil {
		t.Fatal("tags should be non-nil so meta.json says [] not null")
	}

	kept := Meta{URL: "u", CapturedAt: "2020-01-01T00:00:00Z", Source: "monobrowse", Tags: []string{"a"}}
	kept.Normalize(now)
	if kept.CapturedAt != "2020-01-01T00:00:00Z" || kept.Source != "monobrowse" {
		t.Fatalf("Normalize overwrote sender values: %+v", kept)
	}
}

func TestMetaDedupeURL(t *testing.T) {
	m := Meta{URL: "https://example.com/a?utm=1", CanonicalURL: "https://example.com/a"}
	if got := m.DedupeURL(); got != "https://example.com/a" {
		t.Fatalf("DedupeURL = %q", got)
	}
	m.CanonicalURL = "  "
	if got := m.DedupeURL(); got != "https://example.com/a?utm=1" {
		t.Fatalf("DedupeURL fallback = %q", got)
	}
}

func TestMetaMarshalIsIndentable(t *testing.T) {
	m := Meta{URL: "https://example.com/a"}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if !strings.Contains(string(out), "\n  \"url\": \"https://example.com/a\"") {
		t.Fatalf("meta.json is not indented:\n%s", out)
	}
}

func TestMetaCapturedTimeFallback(t *testing.T) {
	fallback := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	m := Meta{CapturedAt: "not a timestamp"}
	if got := m.capturedTime(fallback); !got.Equal(fallback) {
		t.Fatalf("capturedTime = %v, want the fallback", got)
	}
	m.CapturedAt = "2026-09-20T08:00:00Z"
	if got := m.capturedTime(fallback); got.UTC().Hour() != 8 {
		t.Fatalf("capturedTime = %v", got)
	}
}

// TestMetaProfileRoundTrip: the extension sets `profile`, and it must reach
// meta.json unchanged — it is what decides which store ingests the capture.
func TestMetaProfileRoundTrip(t *testing.T) {
	var m Meta
	if err := json.Unmarshal([]byte(`{"url":"https://e.com","profile":"work"}`), &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Profile != "work" {
		t.Fatalf("Profile = %q, want %q", m.Profile, "work")
	}
	blob, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw["profile"] != "work" {
		t.Errorf("profile = %v, want %q", raw["profile"], "work")
	}
}

// An unprofiled capture writes no `profile` key at all, so a meta.json from
// this binary is indistinguishable from one written before the field
// existed — and no reader has to special-case an empty string.
func TestMetaProfileOmittedWhenEmpty(t *testing.T) {
	blob, err := json.Marshal(Meta{URL: "https://e.com"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["profile"]; ok {
		t.Errorf("empty profile was written: %s", blob)
	}
	// Every other field still ships, omitempty or not.
	for _, key := range []string{"url", "canonicalUrl", "title", "byline", "tags", "source"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("field %q went missing: %s", key, blob)
		}
	}
}

func TestMetaNormalizeTrimsProfile(t *testing.T) {
	m := Meta{URL: "https://e.com", Profile: "  work \n"}
	m.Normalize(time.Unix(0, 0).UTC())
	if m.Profile != "work" {
		t.Errorf("Profile = %q, want %q", m.Profile, "work")
	}
}
