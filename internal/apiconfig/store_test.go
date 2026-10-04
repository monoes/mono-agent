package apiconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/monoes/mono-agent/internal/testdb"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	return testdb.Open(t).DB
}

func putRow(t *testing.T, db *sql.DB, value string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, Row, value); err != nil {
		t.Fatal(err)
	}
}

// row is the stored text, and whether there is a row.
func row(t *testing.T, db *sql.DB) (string, bool) {
	t.Helper()
	var v string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, Row).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return v, true
}

func rowJSON(t *testing.T, db *sql.DB) map[string]any {
	t.Helper()
	v, ok := row(t, db)
	if !ok {
		t.Fatal("no row")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		t.Fatalf("the row is not JSON: %v\n%s", err, v)
	}
	return m
}

func TestTheRowIsInTheSettingsTable(t *testing.T) {
	if Row != "api_gateway_config" {
		t.Errorf("Row = %q", Row)
	}
}

func TestLoadOfNothingSavedIsEmpty(t *testing.T) {
	s, err := Load(context.Background(), openDB(t))
	if err != nil || !s.IsEmpty() {
		t.Fatalf("Load = %+v, %v", s, err)
	}
}

func TestUpdateThenLoadRoundTripsEverySetting(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	want := map[string]string{
		"v1_addr": "0.0.0.0:9443", "tls_cert_file": "/etc/c.pem", "tls_key_file": "/etc/k.pem", "confinement": "sandboxed",
		"context_confinement": "any", "auto_confinement": "chat-only", "max_concurrent": "8", "turn_timeout": "15m",
		"image_runtimes": "none", "tool_runtimes": "claude",
	}
	if err := Update(ctx, db, func(s *Settings) error {
		for k, v := range want {
			if err := s.Set(k, v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}

	// The document: v 1, strings, and a number for max_concurrent.
	m := rowJSON(t, db)
	if m["v"] != float64(1) {
		t.Errorf("v = %v", m["v"])
	}
	if m["max_concurrent"] != float64(8) {
		t.Errorf("max_concurrent is stored as %#v, want the number 8", m["max_concurrent"])
	}
	if m["v1_addr"] != "0.0.0.0:9443" || m["image_runtimes"] != "none" {
		t.Errorf("document: %v", m)
	}
	if len(m) != 11 {
		t.Errorf("%d fields, want v and the ten settings: %v", len(m), m)
	}
}

// Update takes any text, checked or not. A number that is not written the way JSON writes
// one is kept as the text it is, and the row stays JSON that can be read back.
func TestATextThatIsNotAJSONNumberIsStoredAsText(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	for _, text := range []string{"007", "+5", "-1", "8.0", "1e1", "99999999999999999999", "abc", " 8"} {
		if err := Update(ctx, db, func(s *Settings) error { return s.Set("max_concurrent", text) }); err != nil {
			t.Fatal(err)
		}
		if m := rowJSON(t, db); m["max_concurrent"] != text {
			t.Errorf("%q was stored as %#v, want the text", text, m["max_concurrent"])
		}
		if got, err := Load(ctx, db); err != nil || got.MaxConcurrent != text {
			t.Errorf("%q came back as %q, %v", text, got.MaxConcurrent, err)
		}
	}
}

func TestUnsettingTheLastSettingRemovesTheRow(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	if err := Update(ctx, db, func(s *Settings) error { return s.Set("confinement", "any") }); err != nil {
		t.Fatal(err)
	}
	if _, ok := row(t, db); !ok {
		t.Fatal("the row should exist")
	}
	if err := Update(ctx, db, func(s *Settings) error { s.Unset("confinement"); return nil }); err != nil {
		t.Fatal(err)
	}
	if v, ok := row(t, db); ok {
		t.Errorf("nothing is saved, and the row is still there: %s", v)
	}
}

// A newer binary may have written fields this one has never heard of. They are not read,
// and they are written back as they were.
func TestUnknownFieldsAreIgnoredOnReadAndKeptOnWrite(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	putRow(t, db, `{"v":1,"future":{"a":[1,2,{"b":null}],"c":"d"},"v1_addr":":9443","also_new":true}`)
	s, err := Load(ctx, db)
	if err != nil || s.V1Addr != ":9443" || s.Get("future") != "" {
		t.Fatalf("Load = %+v, %v", s, err)
	}
	if err := Update(ctx, db, func(s *Settings) error { return s.Set("confinement", "chat-only") }); err != nil {
		t.Fatal(err)
	}
	m := rowJSON(t, db)
	future, _ := json.Marshal(m["future"])
	if string(future) != `{"a":[1,2,{"b":null}],"c":"d"}` || m["also_new"] != true || m["v1_addr"] != ":9443" || m["confinement"] != "chat-only" {
		t.Errorf("the write lost or changed a field: %v", m)
	}

	// Unsetting every setting leaves the fields that are not ours, so the row stays.
	if err := Update(ctx, db, func(s *Settings) error { s.Unset("v1_addr"); s.Unset("confinement"); return nil }); err != nil {
		t.Fatal(err)
	}
	m = rowJSON(t, db)
	if m["also_new"] != true || m["v1_addr"] != nil || m["confinement"] != nil {
		t.Errorf("after the unset: %v", m)
	}
}

func TestADocumentOfANewerFormatIsRefusedAndLeftAsItWas(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	const newer = `{"v":2,"v1_addr":":9443"}`
	putRow(t, db, newer)
	if _, err := Load(ctx, db); !errors.Is(err, ErrTooNew) || !strings.Contains(err.Error(), Row) {
		t.Errorf("Load: %v, want ErrTooNew naming the row", err)
	}
	called := false
	err := Update(ctx, db, func(s *Settings) error { called = true; return s.Set("confinement", "any") })
	if !errors.Is(err, ErrTooNew) || called {
		t.Errorf("Update: %v (fn called %v), want ErrTooNew and the function never called", err, called)
	}
	if v, _ := row(t, db); v != newer {
		t.Errorf("the newer document was changed: %s", v)
	}
}

func TestADamagedDocumentIsAnErrorThatNamesTheRow(t *testing.T) {
	for name, doc := range map[string]string{
		"not json":        `{"v":1,`,
		"an array":        `[1,2]`,
		"a string":        `"x"`,
		"v zero":          `{"v":0}`,
		"v negative":      `{"v":-1}`,
		"v a string":      `{"v":"1"}`,
		"v fractional":    `{"v":1.5}`,
		"v null":          `{"v":null}`,
		"a number as str": `{"v":1,"v1_addr":5}`,
		"an object":       `{"v":1,"confinement":{"a":1}}`,
		"a string number": `{"v":1,"turn_timeout":15}`,
		"a bool":          `{"v":1,"max_concurrent":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			db := openDB(t)
			putRow(t, db, doc)
			_, err := Load(context.Background(), db)
			if err == nil || errors.Is(err, ErrTooNew) || !strings.Contains(err.Error(), Row) {
				t.Fatalf("Load: %v, want an error that names the row", err)
			}
			called := false
			if err := Update(context.Background(), db, func(*Settings) error { called = true; return nil }); err == nil || called {
				t.Errorf("Update went on over a damaged document: %v, fn called %v", err, called)
			}
			if v, _ := row(t, db); v != doc {
				t.Errorf("the damaged document was rewritten: %s", v)
			}
		})
	}
}

func TestFieldsOfTheRightTypeAreRead(t *testing.T) {
	db := openDB(t)
	for _, c := range []struct {
		doc  string
		want string
	}{
		{`{"v":1,"max_concurrent":8}`, "8"},
		{`{"v":1,"max_concurrent":"8"}`, "8"}, // a string holding the number is read too
		{`{"max_concurrent":8}`, "8"},         // a missing v is v 1
		{`{"v":1,"max_concurrent":null}`, ""}, // null is not saved
		{`{"v":1}`, ""},
	} {
		putRow(t, db, c.doc)
		s, err := Load(context.Background(), db)
		if err != nil || s.MaxConcurrent != c.want {
			t.Errorf("%s: MaxConcurrent = %q, %v, want %q", c.doc, s.MaxConcurrent, err, c.want)
		}
	}
}

// An invalid value is the checks' business and not the store's: it must stay readable, so
// that it can be shown and removed.
func TestAnInvalidValueStillLoadsAndCanBeUnset(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	putRow(t, db, `{"v":1,"confinement":"everything","max_concurrent":99}`)
	s, err := Load(ctx, db)
	if err != nil || s.Confinement != "everything" || s.MaxConcurrent != "99" {
		t.Fatalf("Load = %+v, %v", s, err)
	}
	if p := Validate(s); len(p) != 2 {
		t.Errorf("problems: %+v", p)
	}
	if err := Update(ctx, db, func(s *Settings) error { s.Unset("confinement"); return nil }); err != nil {
		t.Fatal(err)
	}
	s, _ = Load(ctx, db)
	if s.Confinement != "" || s.MaxConcurrent != "99" {
		t.Errorf("after the unset: %+v", s)
	}
}

func TestAFailingFunctionRollsBack(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	putRow(t, db, `{"v":1,"v1_addr":":9443"}`)
	boom := errors.New("boom")
	err := Update(ctx, db, func(s *Settings) error {
		_ = s.Set("confinement", "any")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update = %v, want the function's error", err)
	}
	if v, _ := row(t, db); v != `{"v":1,"v1_addr":":9443"}` {
		t.Errorf("the failed update changed the row: %s", v)
	}
	// ...and the write lock is gone: the next update goes through.
	if err := Update(ctx, db, func(s *Settings) error { return s.Set("confinement", "any") }); err != nil {
		t.Errorf("an update after a rollback: %v", err)
	}
}

// Nothing changed, nothing written: the row keeps the bytes it had, even if they are not the
// spelling this binary writes.
func TestAnUpdateThatChangesNothingWritesNothing(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	const hand = "{ \"v\": 1,\n  \"v1_addr\": \":9443\" }"
	putRow(t, db, hand)
	for _, fn := range []func(*Settings) error{
		func(*Settings) error { return nil },
		func(s *Settings) error { return s.Set("v1_addr", ":9443") },
		func(s *Settings) error { s.Unset("confinement"); return nil },
	} {
		if err := Update(ctx, db, fn); err != nil {
			t.Fatal(err)
		}
		if v, _ := row(t, db); v != hand {
			t.Errorf("a no-op update rewrote the row: %q", v)
		}
	}
	// With no row it creates none.
	db2 := openDB(t)
	if err := Update(ctx, db2, func(*Settings) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok := row(t, db2); ok {
		t.Error("an update that changes nothing made a row")
	}
}

func TestACancelledContextWritesNothing(t *testing.T) {
	db := openDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Update(ctx, db, func(s *Settings) error { return s.Set("confinement", "any") }); err == nil {
		t.Error("Update with a cancelled context succeeded")
	}
	if _, ok := row(t, db); ok {
		t.Error("a cancelled update wrote the row")
	}
}

// A dry run runs the function over the stored document and writes nothing.
func TestADryRunWritesNothing(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	putRow(t, db, `{"v":1,"v1_addr":":9443"}`)
	var seen string
	if err := update(ctx, db, true, func(s *Settings) error {
		seen = s.V1Addr
		return s.Set("confinement", "any")
	}); err != nil {
		t.Fatal(err)
	}
	if seen != ":9443" {
		t.Errorf("the dry run saw %q", seen)
	}
	if v, _ := row(t, db); v != `{"v":1,"v1_addr":":9443"}` {
		t.Errorf("the dry run wrote: %s", v)
	}
}

// The reason Update is one transaction: the CLI and the app, or two MCP calls, change
// settings at the same time and neither may lose the other's change.
func TestParallelUpdatesOfDifferentFieldsLoseNothing(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	keys := Keys()
	const rounds = 4
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, len(keys)*rounds)
	for _, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				v := fmt.Sprintf("%s-%d", key, r)
				if err := Update(ctx, db, func(s *Settings) error { return s.Set(key, v) }); err != nil {
					errs <- fmt.Errorf("%s round %d: %w", key, r, err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got, err := Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if want := fmt.Sprintf("%s-%d", key, rounds-1); got.Get(key) != want {
			t.Errorf("%s = %q, want %q: an update was lost", key, got.Get(key), want)
		}
	}
}

// Many writers of one field, each reading the value it increments: 40 increments make 40.
func TestParallelIncrementsOfOneFieldAllCount(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	const writers = 40
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- Update(ctx, db, func(s *Settings) error {
				n, _ := strconv.Atoi(s.MaxConcurrent)
				return s.Set("max_concurrent", strconv.Itoa(n+1))
			})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	got, _ := Load(ctx, db)
	if got.MaxConcurrent != strconv.Itoa(writers) {
		t.Errorf("max_concurrent = %q after %d increments: updates were lost", got.MaxConcurrent, writers)
	}
}
