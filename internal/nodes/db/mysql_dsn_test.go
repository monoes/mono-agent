package dbnodes

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseMySQLDSN(t *testing.T) {
	tests := []struct {
		name     string
		dsn      string
		addr     string
		user     string
		password string
		db       string
		params   url.Values // expected driver params after translation
	}{
		{
			name: "old form with tcp()",
			dsn:  "alice:secret@tcp(db.example.com:3307)/myapp",
			addr: "db.example.com:3307", user: "alice", password: "secret", db: "myapp",
			params: url.Values{},
		},
		{
			name: "old form with parseTime and other ignored params",
			dsn:  "alice:secret@tcp(127.0.0.1:3306)/myapp?parseTime=true&charset=utf8mb4&loc=UTC",
			addr: "127.0.0.1:3306", user: "alice", password: "secret", db: "myapp",
			params: url.Values{},
		},
		{
			name: "new form",
			dsn:  "alice:secret@db.example.com:3307/myapp",
			addr: "db.example.com:3307", user: "alice", password: "secret", db: "myapp",
			params: url.Values{},
		},
		{
			name: "mysql:// scheme",
			dsn:  "mysql://alice:secret@db.example.com:3307/myapp?timeout=5s",
			addr: "db.example.com:3307", user: "alice", password: "secret", db: "myapp",
			params: url.Values{"timeout": {"5s"}},
		},
		{
			name: "default port",
			dsn:  "alice:secret@tcp(db.example.com)/myapp",
			addr: "db.example.com:3306", user: "alice", password: "secret", db: "myapp",
			params: url.Values{},
		},
		{
			name: "default address (old form, empty tcp part)",
			dsn:  "alice:secret@/myapp",
			addr: "127.0.0.1:3306", user: "alice", password: "secret", db: "myapp",
			params: url.Values{},
		},
		{
			name: "no database",
			dsn:  "alice:secret@tcp(db:3306)/",
			addr: "db:3306", user: "alice", password: "secret", db: "",
			params: url.Values{},
		},
		{
			name: "no slash at all (new form)",
			dsn:  "alice:secret@db:3306",
			addr: "db:3306", user: "alice", password: "secret", db: "",
			params: url.Values{},
		},
		{
			name: "user without password",
			dsn:  "alice@tcp(db:3306)/myapp",
			addr: "db:3306", user: "alice", password: "", db: "myapp",
			params: url.Values{},
		},
		{
			name: "password with at-sign, slash, colon and question mark is taken literally",
			dsn:  "alice:p@ss/w:rd?x@tcp(db:3306)/myapp",
			addr: "db:3306", user: "alice", password: "p@ss/w:rd?x", db: "myapp",
			params: url.Values{},
		},
		{
			name: "ipv6 host",
			dsn:  "alice:secret@tcp([::1]:3306)/myapp",
			addr: "[::1]:3306", user: "alice", password: "secret", db: "myapp",
			params: url.Values{},
		},
		{
			name: "ipv6 host without port",
			dsn:  "alice:secret@tcp([::1])/myapp",
			addr: "[::1]:3306", user: "alice", password: "secret", db: "myapp",
			params: url.Values{},
		},
		{
			name: "timeouts, collation and compress pass through",
			dsn:  "u:p@tcp(h:1)/d?timeout=3s&readTimeout=4s&writeTimeout=5s&collation=utf8mb4_unicode_ci&compress=zstd",
			addr: "h:1", user: "u", password: "p", db: "d",
			params: url.Values{"timeout": {"3s"}, "readTimeout": {"4s"}, "writeTimeout": {"5s"}, "collation": {"utf8mb4_unicode_ci"}, "compress": {"zstd"}},
		},
		{
			name: "old boolean compress is mapped",
			dsn:  "u:p@tcp(h:1)/d?compress=true",
			addr: "h:1", user: "u", password: "p", db: "d",
			params: url.Values{"compress": {"zlib"}},
		},
		{
			name: "tls=skip-verify",
			dsn:  "u:p@tcp(h:1)/d?tls=skip-verify",
			addr: "h:1", user: "u", password: "p", db: "d",
			params: url.Values{"tls": {"skip-verify"}},
		},
		{
			name: "tls=true verifies the certificate against the host",
			dsn:  "u:p@tcp(db.example.com:1)/d?tls=true",
			addr: "db.example.com:1", user: "u", password: "p", db: "d",
			params: url.Values{mysqlVerifiedTLSParam: {"db.example.com"}},
		},
		{
			name: "ssl alias, tls=false is a no-op",
			dsn:  "u:p@tcp(h:1)/d?ssl=false",
			addr: "h:1", user: "u", password: "p", db: "d",
			params: url.Values{},
		},
		{
			name: "last duplicate wins, like go-sql-driver",
			dsn:  "u:p@tcp(h:1)/d?timeout=1s&timeout=2s",
			addr: "h:1", user: "u", password: "p", db: "d",
			params: url.Values{"timeout": {"2s"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := parseMySQLDSN(tc.dsn)
			if err != nil {
				t.Fatalf("parseMySQLDSN(%q): %v", tc.dsn, err)
			}
			if c.Addr != tc.addr || c.User != tc.user || c.Password != tc.password || c.DB != tc.db {
				t.Errorf("got addr=%q user=%q password=%q db=%q; want addr=%q user=%q password=%q db=%q",
					c.Addr, c.User, c.Password, c.DB, tc.addr, tc.user, tc.password, tc.db)
			}
			if c.Params.Encode() != tc.params.Encode() {
				t.Errorf("params = %v; want %v", c.Params, tc.params)
			}
		})
	}
}

func TestParseMySQLDSNRejects(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		wantErr string
	}{
		{"unsupported option", "u:p@tcp(h:1)/d?multiStatements=true", `"multiStatements"`},
		{"interpolateParams", "u:p@tcp(h:1)/d?interpolateParams=true", `"interpolateParams"`},
		{"loc other than UTC", "u:p@tcp(h:1)/d?loc=Local", `"loc"`},
		{"charset other than utf8mb4", "u:p@tcp(h:1)/d?charset=latin1", `"charset"`},
		{"tls custom name", "u:p@tcp(h:1)/d?tls=mycfg", `"tls"`},
		{"tls preferred", "u:p@tcp(h:1)/d?tls=preferred", `"tls"`},
		{"bad timeout", "u:p@tcp(h:1)/d?timeout=soon", `"timeout"`},
		{"bad compress", "u:p@tcp(h:1)/d?compress=gzip", `"compress"`},
		{"unix socket", "u:p@unix(/var/run/mysqld.sock)/d", "unix"},
		{"empty", "", "empty"},
		{"unterminated tcp(", "u:p@tcp(h:1/d", "tcp("},
		{"bad query", "u:p@tcp(h:1)/d?a=%zz", "query"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseMySQLDSN(tc.dsn)
			if err == nil {
				t.Fatalf("parseMySQLDSN(%q): expected an error", tc.dsn)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

// The error for an unsupported option lists what is supported, so the user can
// fix the connection string without reading the source.
func TestParseMySQLDSNUnsupportedListsSupported(t *testing.T) {
	_, err := parseMySQLDSN("u:p@tcp(h:1)/d?multiStatements=true")
	if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "tls") {
		t.Fatalf("expected the error to list supported options, got: %v", err)
	}
}

// The error text must never echo the password.
func TestParseMySQLDSNErrorsDoNotLeakPassword(t *testing.T) {
	_, err := parseMySQLDSN("alice:hunter2secret@tcp(h:1)/d?multiStatements=true")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "hunter2secret") {
		t.Fatalf("error leaks the password: %v", err)
	}
}
