package mcp

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/apikeys"
)

// raceSettle gives two calls that were sent together time to read the key and to
// queue for the write lock. A slower server only lets a test pass, never fail.
const raceSettle = 300 * time.Millisecond

// Two tool calls in flight at once, which a host does when it sends two in a turn
// (each runs on a goroutine of its own), must both land. Each used to read the key
// and then write both its columns from what it had read, so one undid the other.
// The write lock is held on another connection while they are sent, so that both
// have read the key before either writes, every time.
func TestTwoUpdateCallsInFlightBothLand(t *testing.T) {
	s, dbPath := newAPIKeyServer(t, true)
	side := sideDB(t, dbPath)
	store := apikeys.NewStore(side.DB)
	k, _, err := store.Create(context.Background(), "default", "app", true)
	if err != nil {
		t.Fatal(err)
	}

	w := newWireSession(t, s)
	w.call("api_key_list", nil) // the server is up before the lock is taken

	tx, err := side.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, "2026-10-02T00:00:00.000Z", k.ID); err != nil {
		t.Fatal(err)
	}
	a := w.send("api_key_update", map[string]any{"id": k.ID, "context": false})
	b := w.send("api_key_update", map[string]any{"id": k.ID, "name": "renamed"})
	time.Sleep(raceSettle)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a, b} {
		if text, isErr := w.await(id); isErr {
			t.Fatalf("an update of an active key failed: %s", scrubbed(text))
		}
	}
	w.finish()

	got, err := store.Get(context.Background(), "default", k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" || got.Context {
		t.Errorf("both calls must land: name %q, context %v; want renamed and off", got.Name, got.Context)
	}
}

// The same, in bulk and without the lock, for the race detector: many keys, each
// with two updates dispatched together, all landing.
func TestManyUpdateCallsAtOnceAllLand(t *testing.T) {
	s, dbPath := newAPIKeyServer(t, true)
	store := apikeys.NewStore(sideDB(t, dbPath).DB)
	const keys = 40
	ids := make([]string, keys)
	var lines []string
	for i := range ids {
		k, _, err := store.Create(context.Background(), "default", "key-"+strconv.Itoa(i), true)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = k.ID
		lines = append(lines,
			callToolReq(2*i+1, "api_key_update", map[string]any{"id": k.ID, "context": false}),
			callToolReq(2*i+2, "api_key_update", map[string]any{"id": k.ID, "name": fmt.Sprintf("renamed-%d", i)}))
	}

	for _, resp := range serveLines(t, s, lines...) {
		if text, isErr := toolText(t, resp); isErr {
			t.Fatalf("an update of an active key failed: %s", scrubbed(text))
		}
	}
	for i, id := range ids {
		got, err := store.Get(context.Background(), "default", id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != fmt.Sprintf("renamed-%d", i) || got.Context {
			t.Errorf("key %d: name %q, context %v; both calls must have landed", i, got.Name, got.Context)
		}
	}
}
