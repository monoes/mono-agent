package workflow

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
)

// A locked account is refused with a 503 and no run, whatever the method or path
// (an unknown webhook looks like a known one), whether the credential is right,
// wrong or missing, for the preflight too, and before the body is read (the last
// request is over the 1 MiB limit an allowed server answers with a 413).
func TestWebhookServerRefusesRequestsWhileLocked(t *testing.T) {
	big := strings.Repeat("x", 2<<20)
	requests := []struct{ method, path, secret, body string }{
		{http.MethodPost, "/webhook/hook", "s3cr3t", `{"a":1}`}, // the one valid call
		{http.MethodPost, "/webhook/hook", "", `{"a":1}`},
		{http.MethodPost, "/webhook/hook", "wrong", `{"a":1}`},
		{http.MethodGet, "/webhook/hook", "s3cr3t", ""},
		{http.MethodPost, "/webhook/nope", "s3cr3t", `{"a":1}`},
		{http.MethodOptions, "/webhook/hook", "", ""},
		{http.MethodPost, "/elsewhere", "", ""},
		{http.MethodPost, "/webhook/hook", "s3cr3t", big},
	}
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			s := NewWebhookServer(":0", zerolog.Nop())
			fired := 0
			if err := s.Register(&WebhookRegistration{
				Path: "hook", Method: "POST", AuthHeader: "X-Webhook-Secret", AuthToken: "s3cr3t",
				TriggerFn: func([]Item) { fired++ },
			}); err != nil {
				t.Fatal(err)
			}
			accounttest.Install(t, c.Mode)

			for i, r := range requests {
				req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
				if r.secret != "" {
					req.Header.Set("X-Webhook-Secret", r.secret)
				}
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, req)
				switch {
				case c.Refused:
					if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "60" ||
						strings.TrimSpace(rec.Body.String()) != `{"error":"login_required"}` {
						t.Errorf("%s %s (secret %q) = %d %q Retry-After %q, want 503 {\"error\":\"login_required\"} with Retry-After 60",
							r.method, r.path, r.secret, rec.Code, rec.Body, rec.Header().Get("Retry-After"))
					}
				case i == 0 && rec.Code != http.StatusOK:
					t.Errorf("the valid webhook call = %d %s, want 200", rec.Code, rec.Body)
				}
			}
			want := 1 // only the first request is a valid call: it runs when the account allows it
			if c.Refused {
				want = 0
			}
			if fired != want {
				t.Fatalf("%d runs started, want %d", fired, want)
			}
		})
	}
}

// The server says once a minute why its callers are getting 503s. The log has a
// clock of its own, which the test steps.
func TestWebhookServerLogsALockedAccountOncePerMinute(t *testing.T) {
	var logs bytes.Buffer
	s := NewWebhookServer(":0", zerolog.New(&logs))
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s.lockedLog.now = func() time.Time { return clock }
	accounttest.Install(t, accounttest.LockedNoLogin)
	refuse := func() {
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/webhook/hook", nil))
	}
	lines := func() int { return strings.Count(logs.String(), "no valid monoes.me login") }

	for i := 0; i < 5; i++ {
		refuse()
	}
	if n := lines(); n != 1 {
		t.Fatalf("logged %d times for 5 refusals, want once:\n%s", n, logs.String())
	}
	clock = clock.Add(webhookLockedLogEvery - time.Second)
	refuse()
	if n := lines(); n != 1 {
		t.Fatalf("logged %d times before a minute passed, want once", n)
	}
	clock = clock.Add(time.Second)
	refuse()
	if n := lines(); n != 2 {
		t.Fatalf("logged %d times once a minute passed, want twice", n)
	}
}
