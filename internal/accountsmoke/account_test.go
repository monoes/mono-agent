//go:build devaccount && !windows

package accountsmoke

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// What the gate says (index §3.4); the smoke asserts these words, never the tokens behind them.
const (
	loginLine       = "Log in to monoes.me first: monoagentcli account login"
	graceLine       = "monoes.me is unreachable; this login works offline until"
	unconfirmedLine = "This login can no longer be renewed on this machine and works until"
	warnLine        = "A monoes.me login will be required from"
	testEmail       = "ada@example.com" // the user the fake's simulated browser is logged in as
)

// past is an enforcement date long gone: the binary enforces from the first second. future is one
// that never comes: the binary is in its warn period and nothing is refused.
var (
	past   = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	future = time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
)

// accountStatus is `account status --json` (spec §7): the fields the smoke reads.
type accountStatus struct {
	State       string    `json:"state"`
	Reason      string    `json:"reason"`
	EnforceFrom time.Time `json:"enforce_from"`
	Enforced    bool      `json:"enforced"`
	User        *struct {
		Email string `json:"email"`
	} `json:"user"`
}

// status runs `account status --json`: the document and the exit code (0 for ok and grace, 4 for locked).
// It reads the first document on stdout: that the command prints only that one is B1b's and B2's to
// pin, and a reader of the state should not fail on what follows it.
func (r *rig) status() (accountStatus, int) {
	r.t.Helper()
	res := r.run("account", "status", "--json")
	var st accountStatus
	if err := json.NewDecoder(strings.NewReader(res.stdout)).Decode(&st); err != nil {
		r.t.Fatalf("account status --json (exit %d) printed no document (%v)\nstdout: %s\nstderr: %s", res.code, err, res.stdout, res.stderr)
	}
	return st, res.code
}

// signIn signs in the way a headless machine does, with the code the fake sends.
func (r *rig) signIn() {
	r.t.Helper()
	mustExit(r.t, r.run("account", "login", "--email", testEmail, "--send"), 0)
	mustExit(r.t, r.run("account", "login", "--email", testEmail, "--code", r.fake.EmailCode), 0)
	if st, code := r.status(); st.State != "ok" || code != 0 || st.User == nil || st.User.Email != testEmail {
		r.t.Fatalf("after signing in: state %q reason %q, exit %d", st.State, st.Reason, code)
	}
}

// accountDir is where the rig's HOME keeps the session.
func (r *rig) accountDir() string { return filepath.Join(r.home, ".monoagent", "account") }

func (r *rig) refreshFile() string { return filepath.Join(r.accountDir(), "refresh.enc") }

// session is the stored session, nil when there is none.
func (r *rig) session() *account.Session {
	r.t.Helper()
	sess, err := account.OpenStore(r.accountDir(), account.NewMemorySealer()).Load()
	must(r.t, err)
	return sess
}

// assertOnlyTheClockGuardRecord checks what a refused command leaves on a machine that never signed in,
// from the enforcement date on (A25): HOME holds the two files of the record and nothing else (no
// first-run marker, no database, no refresh token), and the session in them has no token and a
// high-water mark that lies between the two times the test took the clock.
func (r *rig) assertOnlyTheClockGuardRecord(from, to time.Time) {
	r.t.Helper()
	var files []string
	must(r.t, filepath.WalkDir(r.home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(r.home, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	}))
	if want := []string{".monoagent/account/session.json", ".monoagent/account/session.lock"}; !slices.Equal(files, want) {
		r.t.Fatalf("a refused command left %v in HOME, want %v: the clock-guard record and nothing else", files, want)
	}
	if sess := r.session(); sess == nil || sess.AccessToken != "" || sess.State != "" || sess.HW.Before(from) || sess.HW.After(to) {
		r.t.Fatalf("the record is %+v, want a session with no token and a high-water mark between %v and %v", sess, from, to)
	}
}

// backdate ages the session: its access token becomes one issued age ago (signed with the
// development key, under the kid read from the real token the sign-in produced) and its last
// refresh attempt becomes that old too, which ends the one-minute negative cache of a CLI process,
// so that its next command tries to refresh at once. A real binary has no clock to set, by design
// (no environment variable relaxes the gate), so the smoke ages what the clock is compared with;
// the guard's own tests inject the clock. An age over an hour is an expired token, over 24 hours
// the end of the grace.
func (r *rig) backdate(age time.Duration) {
	r.t.Helper()
	st := account.OpenStore(filepath.Join(r.home, ".monoagent", "account"), account.NewMemorySealer())
	unlock, err := st.Lock(context.Background())
	must(r.t, err)
	defer unlock()
	sess, err := st.Load()
	must(r.t, err)
	if sess == nil {
		r.t.Fatal("no session to age")
	}
	parts := strings.Split(sess.AccessToken, ".")
	if len(parts) != 3 {
		r.t.Fatal("the access token is not a JWS")
	}
	var header struct{ KID string }
	var claims struct{ Sub string }
	for i, into := range []any{&header, &claims} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		must(r.t, err)
		must(r.t, json.Unmarshal(raw, into))
	}
	pub, priv := accounttest.DevKeyPair()
	f := accounttest.New(r.t)
	f.Private, f.Key = priv, account.Key{KID: header.KID, Public: pub}
	sess.AccessToken = f.Token(accounttest.TokenOptions{Sub: claims.Sub, IssuedAt: time.Now().Add(-age), Lifetime: time.Hour})
	sess.LastAttempt = time.Now().Add(-age)
	if sess.LastResult != string(account.ReasonUnconfirmed) { // a machine that dropped its token keeps saying why (A24)
		sess.LastResult = "ok"
	}
	must(r.t, st.Save(sess))
}

// assertLocked checks a refused gated command: exit 4, the fixed first line on stderr and, when
// the command asked for JSON, the one document of index §3.4.
func (r *rig) assertLocked(res result, reason string, wantJSON bool) {
	r.t.Helper()
	mustExit(r.t, res, 4)
	// The rig keeps the key in a file, which makes the CLI warn about it first; that line is the
	// rig's, not the gate's.
	stderr := strings.TrimPrefix(res.stderr, "WARN: file-based keyring fallback in use")
	if stderr != res.stderr {
		_, stderr, _ = strings.Cut(stderr, "\n")
	}
	if first, _, _ := strings.Cut(stderr, "\n"); first != loginLine {
		r.t.Fatalf("the first line on stderr is %q, want %q", first, loginLine)
	}
	if !wantJSON {
		return
	}
	var doc struct {
		LoginRequired bool   `json:"login_required"`
		Code          string `json:"code"`
		Account       struct{ State, Reason string }
	}
	mustJSON(r.t, res.stdout, &doc)
	if !doc.LoginRequired || doc.Code != "auth_or_connection" || doc.Account.State != "locked" || doc.Account.Reason != reason {
		r.t.Fatalf("the JSON error is %+v, want login_required, auth_or_connection, locked(%s)", doc, reason)
	}
}
