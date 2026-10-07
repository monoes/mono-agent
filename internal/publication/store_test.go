package publication

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/data"
	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	migration, err := data.MigrationsFS.ReadFile("migrations/063_publications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	migration, err = data.MigrationsFS.ReadFile("migrations/064_publications_sortable_ts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	return NewStore(db, "p1")
}
func TestRegisterScopeAndIdentity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := Entry{ProfileID: "forged", Platform: "X", Kind: "post", Body: "first", RemoteID: "123", Account: "alice", IdempotencyKey: "request", PublishedAt: "2026-10-01T12:00:00+02:00"}
	first, err := s.Register(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProfileID != "p1" || first.PublishedAt != "2026-10-01T10:00:00Z" || first.RecordedAt == "" || first.Media == nil {
		t.Fatalf("bad normalization: %+v", first)
	}
	e.Body = "replacement"
	second, err := s.Register(ctx, e)
	if err != nil || second.ID != first.ID || second.Body != "first" || second.RecordedAt != first.RecordedAt {
		t.Fatalf("retry changed record: %+v %v", second, err)
	}
	e.IdempotencyKey = "other"
	second, err = s.Register(ctx, e)
	if err != nil || second.ID != first.ID {
		t.Fatalf("remote dedup: %+v %v", second, err)
	}
	other := NewStore(s.db, "p2")
	if _, err = other.Get(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-profile get: %v", err)
	}
	entries, err := other.List(ctx, Filter{})
	if err != nil || len(entries) != 0 {
		t.Fatalf("profile list: %+v %v", entries, err)
	}
	third, err := other.Register(ctx, e)
	if err != nil || third.ID == first.ID {
		t.Fatalf("cross-profile dedup: %+v %v", third, err)
	}
	e.Account = "bob"
	third, err = s.Register(ctx, e)
	if err != nil || third.ID == first.ID {
		t.Fatalf("account identity: %+v %v", third, err)
	}
}
func TestListFiltersPagingAndStats(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, e := range []Entry{
		{Platform: "x", Kind: "post", Body: "Hello", WorkflowID: "w1", AgentID: "a1", PublishedAt: "2026-10-01T01:00:00Z"},
		{Platform: "reddit", Kind: "comment", Title: "second", URL: "https://example.com/2", PublishedAt: "2026-10-02T00:00:00Z"},
		{Platform: "x", Kind: "post", Body: "Hello", WorkflowID: "w1", AgentID: "a1", PublishedAt: "2026-10-03T00:00:00Z"},
	} {
		if _, err := s.Register(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		f     Filter
		count int
	}{{Filter{}, 3}, {Filter{Search: "hello"}, 2}, {Filter{Platform: "x", Kind: "post", WorkflowID: "w1", AgentID: "a1"}, 2}, {Filter{Since: "2026-10-02", Until: "2026-10-03"}, 2}, {Filter{Limit: 1, Offset: 1}, 1}, {Filter{Search: "missing"}, 0}} {
		entries, err := s.List(ctx, c.f)
		if err != nil || len(entries) != c.count || entries == nil {
			t.Errorf("List(%+v): %+v %v", c.f, entries, err)
		}
	}
	entries, err := s.List(ctx, Filter{})
	if err != nil || entries[0].PublishedAt != "2026-10-03T00:00:00Z" {
		t.Fatalf("ordering: %+v %v", entries, err)
	}
	stats, err := s.Stats(ctx)
	if err != nil || stats["total"] != 3 || stats["by_platform"].(map[string]int)["x"] != 2 {
		t.Fatalf("stats: %+v %v", stats, err)
	}
}
func TestValidation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, e := range []Entry{{}, {Platform: "x", Kind: "draft", Body: "text"}, {Platform: "x", Kind: "post"}, {Platform: "x", Kind: "post", Body: "text", PublishedAt: "yesterday"}} {
		if _, err := s.Register(ctx, e); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Register(%+v): %v", e, err)
		}
	}
	for _, f := range []Filter{{Limit: -1}, {Limit: 1001}, {Offset: -1}, {Since: "bad"}, {Until: "bad"}, {Since: "2026-10-03", Until: "2026-10-01"}} {
		if _, err := s.List(ctx, f); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("List(%+v): %v", f, err)
		}
	}
	if _, err := s.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestUntilDateAndSafeURLs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, when := range []string{"2026-10-03T23:59:00Z", "2026-10-04T00:00:00Z"} {
		if _, err := s.Register(ctx, Entry{Platform: "x", Kind: "post", Body: "published", PublishedAt: when}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.List(ctx, Filter{Since: "2026-10-03T12:00:00Z", Until: "2026-10-03"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("whole-day until: %+v %v", rows, err)
	}
	for _, url := range []string{"javascript:alert(1)", "file:///secret", "https://user:pass@example.com/"} {
		if _, err := s.Register(ctx, Entry{Platform: "x", Kind: "post", URL: url}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("unsafe URL %q: %v", url, err)
		}
	}
}

func TestNilDatabase(t *testing.T) {
	s := NewStore(nil, "p1")
	ctx := context.Background()
	if _, err := s.Get(ctx, "id"); err == nil {
		t.Fatal("Get should fail without database")
	}
	if _, err := s.Register(ctx, Entry{}); err == nil {
		t.Fatal("Register should fail without database")
	}
	if _, err := s.List(ctx, Filter{}); err == nil {
		t.Fatal("List should fail without database")
	}
	if _, err := s.Stats(ctx); err == nil {
		t.Fatal("Stats should fail without database")
	}
}

func TestRegisterDeduplicatesLiveURLWithoutRemoteID(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	first, err := store.Register(ctx, Entry{Platform: "blog", Kind: "article", URL: "https://example.com/posts/1", Body: "first"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Register(ctx, Entry{Platform: "blog", Kind: "article", URL: first.URL, Body: "second", IdempotencyKey: "another-observation"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != again.ID || again.Body != "first" {
		t.Fatalf("same publication duplicated: %+v %+v", first, again)
	}
	newPost, err := store.Register(ctx, Entry{Platform: "blog", Kind: "article", URL: "https://example.com/posts/2", Body: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if newPost.ID == first.ID {
		t.Fatal("identical content on different live publication merged")
	}
}

func TestRegisterOpaqueParentIdentifiers(t *testing.T) {
	store := testStore(t)
	for _, parent := range []string{"t1_comment", "urn:li:comment:123", "at://did:plc:abc/app.bsky.feed.post/123"} {
		e, err := store.Register(context.Background(), Entry{Platform: "custom", Kind: "reply", Body: "reply", ParentURL: parent})
		if err != nil || e.ParentURL != parent {
			t.Fatalf("parent %q: %+v %v", parent, e, err)
		}
	}
}

func TestKeysetCursorAndDelete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	// Two entries share a timestamp, to prove the id tie-break.
	for i, at := range []string{"2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z", "2026-10-02T00:00:00Z", "2026-10-03T00:00:00Z", "2026-10-04T00:00:00Z"} {
		if _, err := s.Register(ctx, Entry{Platform: "x", Kind: "post", Body: "b" + string(rune('a'+i)), PublishedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.List(ctx, Filter{})
	if err != nil || len(all) != 5 {
		t.Fatalf("all: %v %v", all, err)
	}
	var got []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		page, next, err := s.ListPage(ctx, Filter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page {
			got = append(got, e.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(got) != 5 {
		t.Fatalf("cursor paging got %d", len(got))
	}
	for i := range got {
		if got[i] != all[i].ID {
			t.Fatalf("cursor order differs at %d", i)
		}
	}
	if _, _, err := s.ListPage(ctx, Filter{Cursor: "!!"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, _, err := s.ListPage(ctx, Filter{Cursor: cursor, Offset: 1}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cursor+offset: %v", err)
	}
	other := NewStore(s.db, "p2")
	if err := other.Delete(ctx, all[1].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-profile delete: %v", err)
	}
	if err := s.Delete(ctx, all[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, all[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted still there: %v", err)
	}
	if err := s.Delete(ctx, all[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// Legacy rows (inserted before migration 064) with sub-millisecond digits must
// page across a boundary without skipping or repeating, and an unparseable
// published_at must map to the same zero time in the backfill and in Go.
func TestLegacySubMillisecondAndUnparseablePaging(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	run := func(name string) {
		m, err := data.MigrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(m)); err != nil {
			t.Fatal(err)
		}
	}
	run("063_publications.sql")
	legacy := []struct{ id, at string }{
		{"a", "2026-10-01T00:00:00.1239Z"},      // would round up to .124
		{"b", "2026-10-01T00:00:00.1236789Z"},   // same millisecond as a
		{"c", "2026-10-01T00:00:00.9996Z"},      // must not carry into the next second
		{"d", "2026-10-01T02:00:00.5009+02:00"}, // offset, truncated .500
		{"e", "2026-10-01T00:00:00Z"},
		{"f", "garbage"},
		{"g", ""},
	}
	for _, r := range legacy {
		if _, err := db.Exec(`INSERT INTO publications (id, profile_id, kind, platform, published_at, recorded_at, idempotency_key) VALUES (?, 'p1', 'post', 'x', ?, ?, ?)`, r.id, r.at, r.at, r.id); err != nil {
			t.Fatal(err)
		}
	}
	run("064_publications_sortable_ts.sql")
	want := map[string]string{
		"a": "2026-10-01T00:00:00.123Z", "b": "2026-10-01T00:00:00.123Z", "c": "2026-10-01T00:00:00.999Z",
		"d": "2026-10-01T00:00:00.500Z", "e": "2026-10-01T00:00:00.000Z",
		"f": "0001-01-01T00:00:00.000Z", "g": "0001-01-01T00:00:00.000Z",
	}
	for _, r := range legacy {
		var ts string
		if err := db.QueryRow("SELECT published_ts FROM publications WHERE id=?", r.id).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		if ts != want[r.id] || ts != sortTS(parseTS(r.at)) {
			t.Fatalf("%s: sql %q want %q go %q", r.id, ts, want[r.id], sortTS(parseTS(r.at)))
		}
	}
	s := NewStore(db, "p1")
	ctx := context.Background()
	for _, size := range []int{1, 2, 3} {
		var got []string
		cursor := ""
		for pages := 0; pages < 20; pages++ {
			page, next, err := s.ListPage(ctx, Filter{Limit: size, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range page {
				got = append(got, e.ID)
			}
			if next == "" {
				break
			}
			cursor = next
		}
		// c(.999) > d(.500) > b,a (.123, id desc) > e(.000) > g,f (zero time, id desc)
		if want := "c d b a e g f"; strings.Join(got, " ") != want {
			t.Fatalf("limit %d: got %v want %s", size, got, want)
		}
	}
}
