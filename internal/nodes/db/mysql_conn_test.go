package dbnodes

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/monoes/mono-agent/internal/workflow"
)

// fakeRows mimics the go-mysql driver's rows type: an unexported struct that
// embeds *mysql.Resultset, which is where the column types live.
type fakeRows struct {
	*mysql.Resultset
	data [][]sqldriver.Value
	step int
}

func (r *fakeRows) Columns() []string { return nil }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []sqldriver.Value) error {
	if r.step >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.step])
	r.step++
	return nil
}

func TestTemporalRowsConvertsDateAndTimeColumns(t *testing.T) {
	fields := []*mysql.Field{
		{Type: mysql.MYSQL_TYPE_DATETIME},
		{Type: mysql.MYSQL_TYPE_TIMESTAMP},
		{Type: mysql.MYSQL_TYPE_DATE},
		{Type: mysql.MYSQL_TYPE_TIME},
		{Type: mysql.MYSQL_TYPE_VARCHAR},
		{Type: mysql.MYSQL_TYPE_DATETIME},
		{Type: mysql.MYSQL_TYPE_DATETIME},
		{Type: mysql.MYSQL_TYPE_DATE},
	}
	inner := &fakeRows{
		Resultset: &mysql.Resultset{Fields: fields},
		data: [][]sqldriver.Value{{
			[]byte("2024-03-05 06:07:08"),
			"2024-03-05 06:07:08.250000",
			[]byte("2024-03-05"),
			[]byte("12:34:56"),
			[]byte("2024-03-05 06:07:08"), // a VARCHAR that merely looks like a date
			nil,
			[]byte("0000-00-00 00:00:00"),
			[]byte("0000-00-00"),
		}},
	}
	rows := newTemporalRows(inner)
	dest := make([]sqldriver.Value, len(fields))
	if err := rows.Next(dest); err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := []sqldriver.Value{
		time.Date(2024, 3, 5, 6, 7, 8, 0, time.UTC),
		time.Date(2024, 3, 5, 6, 7, 8, 250000000, time.UTC),
		time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC),
		[]byte("12:34:56"),
		[]byte("2024-03-05 06:07:08"),
		nil,
		time.Time{},
		time.Time{},
	}
	for i := range want {
		switch w := want[i].(type) {
		case time.Time:
			g, ok := dest[i].(time.Time)
			if !ok || !g.Equal(w) {
				t.Errorf("col %d = %#v; want %v", i, dest[i], w)
			}
		case []byte:
			g, ok := dest[i].([]byte)
			if !ok || string(g) != string(w) {
				t.Errorf("col %d = %#v; want %q", i, dest[i], w)
			}
		case nil:
			if dest[i] != nil {
				t.Errorf("col %d = %#v; want nil", i, dest[i])
			}
		}
	}
	if err := rows.Next(dest); err != io.EOF {
		t.Fatalf("expected io.EOF after the last row, got %v", err)
	}
}

// A value that cannot be parsed is passed through untouched rather than lost.
func TestTemporalRowsLeavesUnparseableValues(t *testing.T) {
	inner := &fakeRows{
		Resultset: &mysql.Resultset{Fields: []*mysql.Field{{Type: mysql.MYSQL_TYPE_DATETIME}}},
		data:      [][]sqldriver.Value{{[]byte("not a date")}},
	}
	dest := make([]sqldriver.Value, 1)
	if err := newTemporalRows(inner).Next(dest); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if string(dest[0].([]byte)) != "not a date" {
		t.Fatalf("got %#v", dest[0])
	}
}

// Rows without go-mysql's Resultset are returned as they are.
func TestNewTemporalRowsWithoutResultset(t *testing.T) {
	var r sqldriver.Rows = &struct{ sqldriver.Rows }{}
	if got := newTemporalRows(r); got != r {
		t.Fatal("expected the original rows when no column types are available")
	}
}

func TestMySQLNamedValue(t *testing.T) {
	ts := time.Date(2024, 3, 5, 6, 7, 8, 0, time.FixedZone("x", 3600))
	nv := &sqldriver.NamedValue{Value: ts}
	if err := mysqlNamedValue(nv); err != nil {
		t.Fatalf("time.Time: %v", err)
	}
	if nv.Value != "2024-03-05 05:07:08" {
		t.Fatalf("time.Time must be sent as UTC, got %#v", nv.Value)
	}

	big := &sqldriver.NamedValue{Value: uint64(math.MaxUint64)}
	if err := mysqlNamedValue(big); err != nil || big.Value != "18446744073709551615" {
		t.Fatalf("big uint64 = %#v, %v", big.Value, err)
	}

	small := &sqldriver.NamedValue{Value: uint64(5)}
	if err := mysqlNamedValue(small); err != sqldriver.ErrSkip {
		t.Fatalf("small uint64 must fall through to database/sql, got %v", err)
	}
	if err := mysqlNamedValue(&sqldriver.NamedValue{Value: "x"}); err != sqldriver.ErrSkip {
		t.Fatalf("string must fall through, got %v", err)
	}
}

func TestMySQLNodeRejectsBadDSNBeforeConnecting(t *testing.T) {
	_, err := (&MySQLNode{}).Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{
		"connection_string": "u:p@tcp(127.0.0.1:1)/d?multiStatements=true",
		"query":             "SELECT 1",
	})
	if err == nil || !strings.Contains(err.Error(), "multiStatements") || !strings.Contains(err.Error(), "db.mysql") {
		t.Fatalf("expected a db.mysql error naming the option, got: %v", err)
	}
}

// TestMySQLIntegration runs the node against a real MySQL/MariaDB server. It is
// skipped unless MONOAGENT_TEST_MYSQL_DSN is set, for example:
//
//	docker run -d --name mysql-test -e MYSQL_ROOT_PASSWORD=pw -e MYSQL_DATABASE=testdb -p 127.0.0.1:33306:3306 mysql:8.4
//	MONOAGENT_TEST_MYSQL_DSN='root:pw@tcp(127.0.0.1:33306)/testdb' go test ./internal/nodes/db -run MySQLIntegration
func TestMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("MONOAGENT_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MONOAGENT_TEST_MYSQL_DSN not set")
	}
	ctx := context.Background()
	node := &MySQLNode{}
	run := func(ctx context.Context, cfg map[string]interface{}) ([]workflow.NodeOutput, error) {
		cfg["connection_string"] = dsn
		return node.Execute(ctx, workflow.NodeInput{}, cfg)
	}
	first := func(t *testing.T, out []workflow.NodeOutput) map[string]interface{} {
		t.Helper()
		if len(out) != 1 || len(out[0].Items) == 0 {
			t.Fatalf("expected one item, got %#v", out)
		}
		return out[0].Items[0].JSON
	}

	// A throwaway table named after the test so reruns and parallel packages do not collide.
	table := "monoagent_swap_it"
	if _, err := run(ctx, map[string]interface{}{"operation": "execute", "query": "DROP TABLE IF EXISTS " + table}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	t.Cleanup(func() {
		_, _ = run(ctx, map[string]interface{}{"operation": "execute", "query": "DROP TABLE IF EXISTS " + table})
	})
	if _, err := run(ctx, map[string]interface{}{"operation": "execute", "query": "CREATE TABLE " + table + " (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, i INT, u BIGINT UNSIGNED, f DOUBLE, d DECIMAL(10,2), s VARCHAR(50), b BLOB, dt DATETIME, dd DATE, tm TIME, n INT NULL, flag TINYINT(1)) CHARSET utf8mb4"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	out, err := run(ctx, map[string]interface{}{
		"operation": "insert",
		"query":     "INSERT INTO " + table + " (i,u,f,d,s,b,dt,dd,tm,n,flag) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		"params":    []interface{}{-5, "18446744073709551615", 1.5, "12.34", "héllo", "\x00\x01blob", "2024-03-05 06:07:08", "2024-03-05", "12:34:56", nil, true},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if got := first(t, out); got["rows_affected"] != int64(1) || got["last_insert_id"] != int64(1) {
		t.Fatalf("insert result = %#v", got)
	}

	out, err = run(ctx, map[string]interface{}{"operation": "select", "query": "SELECT * FROM " + table + " WHERE id = ?", "params": []interface{}{1}})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	row := first(t, out)
	checks := map[string]interface{}{
		"i": int64(-5), "f": 1.5, "d": "12.34", "s": "héllo", "b": "\x00\x01blob",
		"dt": time.Date(2024, 3, 5, 6, 7, 8, 0, time.UTC), "dd": time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC),
		"tm": "12:34:56", "n": nil, "flag": int64(1),
	}
	for k, want := range checks {
		got := row[k]
		if wt, ok := want.(time.Time); ok {
			gt, ok := got.(time.Time)
			if !ok || !gt.Equal(wt) {
				t.Errorf("%s = %#v; want %v", k, got, wt)
			}
			continue
		}
		if got != want {
			t.Errorf("%s = %#v (%T); want %#v", k, got, got, want)
		}
	}
	if fmtU := row["u"]; fmtU != uint64(math.MaxUint64) && fmtU != "18446744073709551615" {
		t.Errorf("u = %#v", fmtU)
	}

	// Table-builder mode with placeholders.
	if _, err := run(ctx, map[string]interface{}{"operation": "insert", "table": table, "data": map[string]interface{}{"i": 7, "s": "built"}}); err != nil {
		t.Fatalf("table insert: %v", err)
	}
	out, err = run(ctx, map[string]interface{}{"operation": "select", "table": table, "where": "s = ?", "params": []interface{}{"built"}})
	if err != nil || first(t, out)["i"] != int64(7) {
		t.Fatalf("table select = %#v, %v", out, err)
	}

	// A failing query reports the server's error code and text.
	_, err = run(ctx, map[string]interface{}{"operation": "select", "query": "SELECT * FROM monoagent_no_such_table"})
	if err == nil || !strings.Contains(err.Error(), "db.mysql: query failed:") || !strings.Contains(err.Error(), "1146") {
		t.Fatalf("expected a query failed error with code 1146, got: %v", err)
	}
	_, err = run(ctx, map[string]interface{}{"operation": "execute", "query": "INSERT INTO " + table + " (id) VALUES (1)"})
	if err == nil || !strings.Contains(err.Error(), "db.mysql: exec failed:") || !strings.Contains(err.Error(), "1062") {
		t.Fatalf("expected an exec failed error with code 1062, got: %v", err)
	}

	// Context cancellation interrupts a running query promptly.
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = run(cctx, map[string]interface{}{"operation": "select", "query": "SELECT SLEEP(5)"})
	if err == nil {
		t.Fatal("expected the cancelled query to fail")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %v", elapsed)
	}

	// database/sql transactions through the same connector.
	c, err := parseMySQLDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(newMySQLConnector(c))
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" (i) VALUES (99)"); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE i = 99").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rolled-back row visible: n=%d err=%v", n, err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tx.ExecContext(ctx, "INSERT INTO "+table+" (i) VALUES (98)")
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE i = 98").Scan(&n); err != nil || n != 1 {
		t.Fatalf("committed row missing: n=%d err=%v", n, err)
	}
}
