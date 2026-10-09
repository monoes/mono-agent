package dbnodes

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	mydriver "github.com/go-mysql-org/go-mysql/driver"
)

// mysqlVerifiedTLSParam is the driver option (registered in mysql_conn.go)
// that turns on TLS with certificate and host name verification. Its value is
// the host name to verify. go-mysql-org's own tls=true skips verification, so
// tls=true is translated to this instead to keep go-sql-driver's behaviour.
const mysqlVerifiedTLSParam = "monoagentTLSVerify"

// mysqlSupportedOptions is shown in the error for an unsupported option.
const mysqlSupportedOptions = "tls (true|false|skip-verify), ssl (alias of tls), timeout, readTimeout, writeTimeout, collation, compress (zlib|zstd|uncompressed), retries (on|off); " +
	"parseTime, charset=utf8mb4 and loc=UTC are accepted and have no effect"

// parseMySQLDSN turns a db.mysql connection string into a go-mysql-org driver
// connector. Two forms are accepted:
//
//	user:pass@tcp(host:port)/db?param=value   (go-sql-driver/mysql, the form used so far)
//	user:pass@host:port/db?param=value        (go-mysql-org/go-mysql), optionally prefixed with mysql://
//
// Credentials are taken literally, never percent-decoded: the last "@" ends
// them, the first ":" splits user from password, as go-sql-driver does. The
// port defaults to 3306 and the host to 127.0.0.1. Options go-mysql-org does
// not implement are rejected, never ignored.
func parseMySQLDSN(dsn string) (mydriver.Connector, error) {
	c := mydriver.Connector{Params: url.Values{}}
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return c, fmt.Errorf("connection string is empty")
	}
	dsn = strings.TrimPrefix(dsn, "mysql://")

	head, query := dsn, ""
	if i := strings.LastIndexByte(dsn, '/'); i >= 0 {
		head = dsn[:i]
		c.DB = dsn[i+1:]
		if j := strings.IndexByte(c.DB, '?'); j >= 0 {
			c.DB, query = c.DB[:j], c.DB[j+1:]
		}
	}

	hostPart := head
	if i := strings.LastIndexByte(head, '@'); i >= 0 {
		hostPart = head[i+1:]
		c.User, c.Password, _ = strings.Cut(head[:i], ":")
	}

	addr, err := mysqlAddress(hostPart)
	if err != nil {
		return c, err
	}
	c.Addr = addr

	values, err := url.ParseQuery(query)
	if err != nil {
		return c, fmt.Errorf("invalid options in the connection string query: %w", err)
	}
	for _, key := range sortedKeys(values) {
		if err := applyMySQLOption(&c, key, values[key][len(values[key])-1]); err != nil {
			return c, err
		}
	}
	return c, nil
}

// mysqlAddress accepts "tcp(host:port)", "host:port", "host" and "", and
// returns host:port with the defaults filled in.
func mysqlAddress(s string) (string, error) {
	if i := strings.IndexByte(s, '('); i >= 0 {
		if !strings.HasSuffix(s, ")") {
			return "", fmt.Errorf("invalid address %q: missing ) after tcp(", s)
		}
		if proto := s[:i]; proto != "tcp" {
			return "", fmt.Errorf("network %q is not supported (only tcp; unix sockets are not supported by the go-mysql-org driver)", proto)
		}
		s = s[i+1 : len(s)-1]
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		host, port = strings.Trim(s, "[]"), "3306"
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "3306"
	}
	return net.JoinHostPort(host, port), nil
}

func applyMySQLOption(c *mydriver.Connector, key, value string) error {
	bad := func(want string) error {
		return fmt.Errorf("connection string option %q has unsupported value %q (supported: %s)", key, value, want)
	}
	switch key {
	case "tls", "ssl":
		switch value {
		case "false":
		case "skip-verify":
			c.Params.Set("tls", "skip-verify")
		case "true":
			host, _, _ := net.SplitHostPort(c.Addr)
			c.Params.Set(mysqlVerifiedTLSParam, host)
		default:
			return bad("true, false, skip-verify; named or custom TLS configurations and preferred are not supported")
		}
	case "timeout", "readTimeout", "writeTimeout":
		if _, err := time.ParseDuration(value); err != nil {
			return bad("a duration such as 5s or 1m30s")
		}
		c.Params.Set(key, value)
	case "collation":
		c.Params.Set(key, value)
	case "compress":
		switch value {
		case "true", "zlib":
			c.Params.Set(key, "zlib")
		case "zstd", "uncompressed":
			c.Params.Set(key, value)
		case "false":
		default:
			return bad("true, false, zlib, zstd, uncompressed")
		}
	case "retries":
		if value != "on" && value != "off" {
			return bad("on, off")
		}
		c.Params.Set(key, value)
	case "parseTime":
		// DATE, DATETIME and TIMESTAMP columns always come back as times.
	case "charset":
		if !strings.EqualFold(value, "utf8mb4") {
			return bad("utf8mb4, the default; use collation to pick another")
		}
	case "loc":
		if !strings.EqualFold(value, "UTC") {
			return bad("UTC, the default")
		}
	default:
		return fmt.Errorf("connection string option %q is not supported by the go-mysql-org MySQL driver (supported: %s)", key, mysqlSupportedOptions)
	}
	return nil
}

func sortedKeys(v url.Values) []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
