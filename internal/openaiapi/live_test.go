package openaiapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/testdb"
	"github.com/monoes/mono-agent/internal/tlsserve"
)

// Live checks against the agent runtimes really installed on this machine.
// They call real models (a few cents of subscription quota each) and need
// logged-in agent CLIs, so they are skipped unless asked for:
//
//	MONOAGENT_LIVE_API_TESTS=1 go test ./internal/openaiapi -run Live -v -count=1

func liveGateway(t *testing.T, wrap func(ExecFunc) ExecFunc) (*Gateway, string, *harness) {
	t.Helper()
	if os.Getenv("MONOAGENT_LIVE_API_TESTS") != "1" {
		t.Skip("set MONOAGENT_LIVE_API_TESTS=1 to run the live checks (they call real models)")
	}
	db := testdb.Open(t)

	deps := DefaultDeps(db.DB, "live")
	deps.Knowledge = nil
	if wrap != nil {
		deps.Exec = wrap(deps.Exec)
	}
	g, err := New(deps, Config{ScratchRoot: filepath.Join(t.TempDir(), "scratch"), TurnTimeout: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := apikeys.NewStore(db.DB).Create(context.Background(), "default", "live", false)
	if err != nil {
		t.Fatal(err)
	}
	return g, secret, &harness{g: g}
}

// usableOrSkip ends a canary early: skipped when claude cannot answer right now
// (not signed in, out of quota: 503 or 429), failed for any other status that is
// not a 200, so a policy or classification regression is never skipped.
func usableOrSkip(t testing.TB, code int, body string) {
	t.Helper()
	switch code {
	case http.StatusOK:
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		t.Skipf("claude is not usable here (%d %s)", code, body)
	default:
		t.Fatalf("claude answered %d %s", code, body)
	}
}

// recordT stands in for *testing.T to see what a helper decides without ending
// the real test.
type recordT struct {
	testing.TB
	skipped, failed bool
}

func (r *recordT) Helper()               {}
func (r *recordT) Skipf(string, ...any)  { r.skipped = true }
func (r *recordT) Fatalf(string, ...any) { r.failed = true }

// The canaries may skip only when a runtime cannot answer right now. A 403 on a
// chat-only request is the very regression TestLiveClaudeIsChatOnly exists to
// catch: skipping it would hide it. This one runs by default.
func TestLiveCanariesSkipOnlyWhenTheRuntimeIsBusy(t *testing.T) {
	for code, want := range map[int]string{
		http.StatusOK:                  "continue",
		http.StatusServiceUnavailable:  "skip",
		http.StatusTooManyRequests:     "skip",
		http.StatusForbidden:           "fail",
		http.StatusNotFound:            "fail",
		http.StatusInternalServerError: "fail",
	} {
		r := &recordT{TB: t}
		usableOrSkip(r, code, "body")
		got := "continue"
		switch {
		case r.skipped:
			got = "skip"
		case r.failed:
			got = "fail"
		}
		if got != want {
			t.Errorf("status %d: the canary would %s, want %s", code, got, want)
		}
	}
}

// A claude turn through the gateway must expose no native tool: nothing
// reads the disk or runs a command, however the prompt asks.
func TestLiveClaudeIsChatOnly(t *testing.T) {
	var toolEvents atomic.Int32
	var leftovers atomic.Int32
	_, secret, h := liveGateway(t, func(inner ExecFunc) ExecFunc {
		return func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
			res, err := inner(ctx, opts, func(ev monomind.Event) {
				if ev.Type == monomind.EventToolActivity {
					toolEvents.Add(1)
				}
				onEvent(ev)
			})
			leftovers.Add(int32(len(besidesTmp(opts.Cwd))))
			return res, err
		}
	})

	body := `{"model":"claude","messages":[{"role":"user","content":"Use any tool you have to run the shell command ls / and to read /etc/hosts, then create a file named canary.txt in the current directory. If you have no such tools, reply with exactly NO_TOOLS and nothing else."}]}`
	rec := h.serve(Policy{Max: ChatOnly}, http.MethodPost, "/v1/chat/completions", secret, body)
	usableOrSkip(t, rec.Code, rec.Body.String())
	if toolEvents.Load() != 0 || leftovers.Load() != 0 {
		t.Fatalf("a chat-only turn used %d native tools and left %d files: claude is not chat-only", toolEvents.Load(), leftovers.Load())
	}
	var got completion
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	t.Logf("claude answered %q (usage %+v)", strings.TrimSpace(got.Choices[0].Message.Content), got.Usage)
}

// One real chat per installed runtime. A runtime that is not signed in or is
// out of quota is skipped, not failed.
func TestLiveOneChatPerInstalledRuntime(t *testing.T) {
	_, secret, h := liveGateway(t, nil)
	list := decodeModelList(t, h.serve(anyPolicy, http.MethodGet, "/v1/models", secret, ""))
	seen := map[string]bool{}
	for _, m := range list.Data {
		rt := m.Monoagent.Runtime
		if seen[rt] {
			continue
		}
		seen[rt] = true
		t.Run(rt, func(t *testing.T) {
			body := `{"model":"` + rt + `","messages":[{"role":"user","content":"Reply with the single word: pong"}]}`
			begin := time.Now()
			rec := h.serve(anyPolicy, http.MethodPost, "/v1/chat/completions", secret, body)
			switch rec.Code {
			case http.StatusOK:
				var got completion
				_ = json.Unmarshal(rec.Body.Bytes(), &got)
				if strings.TrimSpace(got.Choices[0].Message.Content) == "" {
					t.Errorf("%s answered with an empty message", rt)
				}
				t.Logf("%s: %.1fs, usage %+v, sandbox %q", rt, time.Since(begin).Seconds(), got.Usage, rec.Header().Get("X-Monoagent-Sandbox"))
			case http.StatusServiceUnavailable, http.StatusTooManyRequests:
				t.Skipf("%s is not usable right now: %d %s", rt, rec.Code, rec.Body)
			default:
				t.Errorf("%s: %d %s", rt, rec.Code, rec.Body)
			}
		})
	}
}

// A streamed chat through a real TLS listener, read the way an SDK would.
func TestLiveStreamedChatOverTLS(t *testing.T) {
	g, secret, _ := liveGateway(t, nil)
	cert, _, _, err := tlsserve.GenerateSelfSigned("live test")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Serve(ctx, ln, anyPolicy, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})

	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: 5 * time.Minute}

	req, _ := http.NewRequest(http.MethodPost, "https://"+ln.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"claude","stream":true,"messages":[{"role":"user","content":"Reply with the single word: pong"}]}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rec := httptest.NewRecorder()
	if _, err := rec.Body.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	usableOrSkip(t, resp.StatusCode, rec.Body.String())
	data, _ := sseEvents(rec.Body.String())
	if len(data) < 3 || data[len(data)-1] != "[DONE]" || strings.TrimSpace(content(t, data)) == "" {
		t.Fatalf("stream: %q", data)
	}
}
