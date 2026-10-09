package dbnodes

import (
	"context"
	"crypto/tls"
	sqldriver "database/sql/driver"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/go-mysql-org/go-mysql/client"
	mydriver "github.com/go-mysql-org/go-mysql/driver"
	"github.com/go-mysql-org/go-mysql/mysql"
)

// The go-mysql-org driver returns DATE, DATETIME and TIMESTAMP columns as text
// and rejects time.Time and uint64 values with the high bit set as query
// parameters. go-sql-driver (used before, with parseTime=true) returned times
// and accepted both. This file closes that gap so the node's results and
// parameters stay the same, and adds verified TLS for tls=true.

func init() {
	mydriver.SetDSNOptions(map[string]mydriver.DriverOption{
		mysqlVerifiedTLSParam: func(c *client.Conn, host string) error {
			c.SetTLSConfig(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
			return nil
		},
	})
	mydriver.AddNamedValueChecker(mysqlNamedValue)
}

// mysqlNamedValue lets time.Time (sent as UTC, go-sql-driver's default
// location) and uint64 values above MaxInt64 (sent as decimal text) through.
func mysqlNamedValue(nv *sqldriver.NamedValue) error {
	switch v := nv.Value.(type) {
	case time.Time:
		nv.Value = v.UTC().Format("2006-01-02 15:04:05.999999")
		return nil
	case uint64:
		if v > math.MaxInt64 {
			nv.Value = strconv.FormatUint(v, 10)
			return nil
		}
	}
	return sqldriver.ErrSkip
}

// mysqlConnector wraps the driver's connector so every connection reports
// date columns as time.Time.
type mysqlConnector struct{ inner mydriver.Connector }

func newMySQLConnector(c mydriver.Connector) sqldriver.Connector { return mysqlConnector{inner: c} }

func (c mysqlConnector) Driver() sqldriver.Driver { return c.inner.Driver() }

func (c mysqlConnector) Connect(ctx context.Context) (sqldriver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	inner, ok := conn.(*mydriver.Conn)
	if !ok {
		return conn, nil
	}
	return &temporalConn{Conn: inner}, nil
}

// temporalConn embeds the driver's connection, so every optional interface it
// implements (context, ping, transactions, argument checking, validity) is
// still seen by database/sql; only the two query methods are overridden.
type temporalConn struct{ *mydriver.Conn }

func (c *temporalConn) QueryContext(ctx context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	rows, err := c.Conn.QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return newTemporalRows(rows), nil
}

func (c *temporalConn) Query(query string, args []sqldriver.Value) (sqldriver.Rows, error) {
	rows, err := c.Conn.Query(query, args)
	if err != nil {
		return nil, err
	}
	return newTemporalRows(rows), nil
}

// temporalRows converts the text of DATE, DATETIME and TIMESTAMP values to
// time.Time (UTC); a zero date becomes the zero time.Time, as in go-sql-driver.
type temporalRows struct {
	sqldriver.Rows
	temporal []bool
}

// newTemporalRows reads the column types from the *mysql.Resultset that the
// driver's rows embed. If they cannot be found the rows are returned as they are.
func newTemporalRows(rows sqldriver.Rows) sqldriver.Rows {
	v := reflect.ValueOf(rows)
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return rows
	}
	f := v.FieldByName("Resultset")
	if !f.IsValid() || !f.CanInterface() {
		return rows
	}
	rs, ok := f.Interface().(*mysql.Resultset)
	if !ok || rs == nil {
		return rows
	}
	temporal := make([]bool, len(rs.Fields))
	hasTemporal := false
	for i, fld := range rs.Fields {
		switch fld.Type {
		case mysql.MYSQL_TYPE_DATE, mysql.MYSQL_TYPE_NEWDATE, mysql.MYSQL_TYPE_DATETIME, mysql.MYSQL_TYPE_TIMESTAMP:
			temporal[i], hasTemporal = true, true
		}
	}
	if !hasTemporal {
		return rows
	}
	return &temporalRows{Rows: rows, temporal: temporal}
}

func (r *temporalRows) Next(dest []sqldriver.Value) error {
	if err := r.Rows.Next(dest); err != nil {
		return err
	}
	for i := range dest {
		if i >= len(r.temporal) || !r.temporal[i] {
			continue
		}
		var s string
		switch v := dest[i].(type) {
		case []byte:
			s = string(v)
		case string:
			s = v
		default:
			continue
		}
		if t, ok := parseMySQLTime(s); ok {
			dest[i] = t
		}
	}
	return nil
}

func parseMySQLTime(s string) (time.Time, bool) {
	if len(s) >= 10 && s[:10] == "0000-00-00" {
		return time.Time{}, true
	}
	layout := "2006-01-02 15:04:05.999999999"
	if len(s) == 10 {
		layout = "2006-01-02"
	}
	t, err := time.Parse(layout, s)
	return t, err == nil
}
