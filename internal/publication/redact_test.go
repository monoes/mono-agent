package publication

import (
	"context"
	"errors"
	"testing"
)

func TestRedactKeepsEntryAndIsProfileScoped(t *testing.T) {
	mine := testStore(t)
	other := NewStore(mine.db, "other")
	ctx := context.Background()
	e, err := mine.Register(ctx, Entry{Platform: "blog", Kind: "post", Title: "Secret title", Body: "secret body", URL: "https://example.com/p", Media: []string{"/tmp/secret-photo.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Redact(ctx, e.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-profile redact: %v", err)
	}
	if got, _ := mine.Get(ctx, e.ID); got.Body != "secret body" {
		t.Fatal("other profile changed the entry")
	}
	r, err := mine.Redact(ctx, e.ID, false)
	if err == nil && len(r.Media) != 0 {
		t.Fatalf("media survived a redact: %v", r.Media)
	}
	if err != nil || r.Body != RedactedMarker || r.Title != "Secret title" || r.URL != e.URL || r.PublishedAt != e.PublishedAt || r.RecordedAt != e.RecordedAt {
		t.Fatalf("body redact: %+v %v", r, err)
	}
	r, err = mine.Redact(ctx, e.ID, true)
	if err != nil || r.Title != RedactedMarker || r.Platform != "blog" || r.Kind != "post" {
		t.Fatalf("title redact: %+v %v", r, err)
	}
	if found, _ := mine.List(ctx, Filter{Search: "secret"}); len(found) != 0 {
		t.Fatal("old content still searchable")
	}
	if _, err = mine.Redact(ctx, "missing", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}
