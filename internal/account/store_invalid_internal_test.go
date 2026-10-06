package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// readDeadline is how long a test waits for a read of the store before it fails:
// a read that waits for a FIFO's writer must fail the test, not hang the suite.
const readDeadline = 10 * time.Second

// within runs fn on a goroutine of its own and fails the test if it has not
// returned within readDeadline.
func within[T any](t *testing.T, what string, fn func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- fn() }()
	select {
	case v := <-done:
		return v
	case <-time.After(readDeadline):
		t.Fatalf("%s did not return within %v", what, readDeadline)
	}
	var zero T
	return zero
}

type loaded struct {
	sess *Session
	err  error
}

func loadWithin(t *testing.T, st Store) loaded {
	t.Helper()
	return within(t, "Load", func() loaded { s, err := st.Load(); return loaded{s, err} })
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// paddedSession is a valid session.json of exactly size bytes: a session followed
// by spaces, which JSON allows after the value.
func paddedSession(t *testing.T, size int) []byte {
	t.Helper()
	data, err := json.Marshal(&Session{V: sessionVersion, Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > size {
		t.Fatalf("a session is %d bytes, more than %d", len(data), size)
	}
	return append(data, bytes.Repeat([]byte(" "), size-len(data))...)
}

// Every Load failure that means session.json is there and cannot be used is
// errSessionInvalid, with the message it has always had: a later change lets a
// long-running guard drop its cached session on it, as a new process judges the
// file. A failure to open or to read the file is not, and keeps its message: it
// says nothing about what the file holds.
func TestLoadMarksAnUnusableSessionFileButNotAFailedRead(t *testing.T) {
	jsonErr := json.Unmarshal([]byte("{"), &Session{})
	for _, c := range []struct {
		name  string
		write func(path string) error
		msg   string
	}{
		{"not JSON", func(p string) error { return os.WriteFile(p, []byte("{"), 0o600) }, fmt.Sprintf("account: session.json is not valid: %v", jsonErr)},
		{"another version", func(p string) error { return os.WriteFile(p, []byte(`{"v":2}`), 0o600) }, "account: session.json has version 2, this build reads version 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := c.write(filepath.Join(dir, sessionFile)); err != nil {
				t.Fatal(err)
			}
			l := loadWithin(t, OpenStore(dir, NewMemorySealer()))
			if l.sess != nil || !errors.Is(l.err, errSessionInvalid) {
				t.Errorf("a session %v, %v, want errSessionInvalid", l.sess != nil, l.err)
			}
			if l.err == nil || l.err.Error() != c.msg {
				t.Errorf("message %q, want %q", l.err, c.msg)
			}
		})
	}

	for _, c := range []struct {
		name  string
		setup func(t *testing.T, root string) (dir string)
	}{
		{"a session.json it may not open", func(t *testing.T, root string) string {
			if runtime.GOOS == "windows" {
				t.Skip("Unix file modes")
			}
			if os.Geteuid() == 0 {
				t.Skip("root opens a file whatever its mode")
			}
			path := filepath.Join(root, sessionFile)
			writeFile(t, path, paddedSession(t, 64))
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			return root
		}},
		{"a regular file where the directory should be", func(t *testing.T, root string) string {
			if runtime.GOOS == "windows" {
				t.Skip("Windows reports a path through a file as not found, which is no session at all")
			}
			blocker := filepath.Join(root, "account")
			writeFile(t, blocker, []byte("in the way"))
			return blocker
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := loadWithin(t, OpenStore(c.setup(t, t.TempDir()), NewMemorySealer()))
			if l.sess != nil || l.err == nil || errors.Is(l.err, errSessionInvalid) {
				t.Errorf("a session %v, %v, want an error that is not errSessionInvalid", l.sess != nil, l.err)
			}
			if l.err != nil && !strings.HasPrefix(l.err.Error(), "account: reading session.json: ") {
				t.Errorf("message %q, want the read's own message", l.err)
			}
		})
	}
}

// The third security review's odd session.json contents: a new process never
// allows without a valid token, whatever the file holds.
func TestOddSessionFilesNeverAllowWithoutAValidToken(t *testing.T) {
	SetEnforceFromForTest(t, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	expired := mintToken(t, time.Now().Add(-48*time.Hour))
	for name, body := range map[string]string{
		"duplicate state keys":     `{"v":1,"state":"refused","state":""}`,
		"unknown fields":           `{"v":1,"plan":"pro","enforced":false,"allowed":true,"state":"ok"}`,
		"v as a float":             `{"v":1.0}`,
		"v huge":                   `{"v":1e400}`,
		"v negative":               `{"v":-1}`,
		"token of the wrong type":  `{"v":1,"access_token":123}`,
		"hw at the end of time":    `{"v":1,"hw":"9999-12-31T23:59:59Z"}`,
		"hw of year 1":             `{"v":1,"hw":"0001-01-01T00:00:00Z","access_token":"a.b.c"}`,
		"state in another case":    `{"v":1,"state":"Refused","access_token":"` + expired + `"}`,
		"an expired valid token":   `{"v":1,"access_token":"` + expired + `"}`,
		"a JSON array":             `[{"v":1}]`,
		"null":                     `null`,
		"a BOM before the object":  "\ufeff" + `{"v":1}`,
		"nested user with a token": `{"v":1,"user":{"id":"x","access_token":"` + expired + `"}}`,
		"last_result says ok":      `{"v":1,"last_result":"ok","access_token":"` + expired + `"}`,
	} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, sessionFile), []byte(body))
		g := NewGuard(GuardOptions{Store: OpenStore(dir, NewMemorySealer())})
		if err := within(t, "Require", func() error { return g.Require(context.Background()) }); !IsLoginRequired(err) {
			t.Errorf("%s: Require = %v, want a refusal", name, err)
		}
	}
}
