package accountdoor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
)

func TestWriteUnauthorizedBody(t *testing.T) {
	if Message != "Log in to monoes.me first: monoagentcli account login" || Code != "login_required" {
		t.Errorf("Message = %q, Code = %q", Message, Code)
	}
	rec := httptest.NewRecorder()
	WriteUnauthorized(rec, &account.LoginRequiredError{Status: account.Status{State: account.StateLocked, Reason: account.ReasonRefused}})

	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q: the bearer is not what was refused", got)
	}
	const want = `{"error":"login_required","login_required":true,"account":{"state":"locked","reason":"refused"}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
}

// account.Require refuses with a *LoginRequiredError, whose verdict the body
// carries. Any other error is described by the current verdict, so the body
// always says what the account is.
func TestWriteUnauthorizedDescribesAnyOtherErrorByTheCurrentVerdict(t *testing.T) {
	accounttest.Install(t, accounttest.LockedRefused)
	rec := httptest.NewRecorder()
	WriteUnauthorized(rec, errors.New("not a verdict"))

	const want = `{"error":"login_required","login_required":true,"account":{"state":"locked","reason":"refused"}}`
	if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusUnauthorized || got != want {
		t.Errorf("%d %s\nwant 401 %s", rec.Code, got, want)
	}
}

// Both shapes go to callers that hold no login, some of them unauthenticated:
// they say what the account is and why, never who.
func TestNoBodyNamesTheUser(t *testing.T) {
	st := account.Status{
		State: account.StateGrace, Reason: account.ReasonUnreachable, Enforced: true,
		User:       &account.User{ID: "user-123", Email: "someone@example.test", Username: "someone"},
		ValidUntil: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
	}
	rec := httptest.NewRecorder()
	WriteUnauthorized(rec, &account.LoginRequiredError{Status: st})
	summary, _ := json.Marshal(SummaryOf(st))
	for _, body := range []string{rec.Body.String(), string(summary)} {
		for _, leak := range []string{"user-123", "someone", "example.test", `"user"`, `"email"`} {
			if strings.Contains(body, leak) {
				t.Errorf("a door body names the user (%q): %s", leak, body)
			}
		}
	}
	bare, _ := json.Marshal(SummaryOf(account.Status{State: account.StateLocked, Reason: account.ReasonNotLoggedIn}))
	if string(summary) != `{"state":"grace","reason":"unreachable","valid_until":"2026-10-05T12:00:00Z","enforced":true}` ||
		string(bare) != `{"state":"locked","reason":"not_logged_in","enforced":false}` {
		t.Errorf("summaries = %s and %s", summary, bare)
	}
}

// Require is the one verdict the doors share: refused exactly where the table
// says, always as a *LoginRequiredError.
func TestRequireInEveryState(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			accounttest.Install(t, c.Mode)
			err := account.Require(context.Background())
			if (err != nil) != c.Refused || (err != nil && !account.IsLoginRequired(err)) {
				t.Fatalf("Require = %v (%T), want refused = %v", err, err, c.Refused)
			}
		})
	}
}
