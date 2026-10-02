package openaiapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
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
	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/testdb"
	"github.com/monoes/mono-agent/internal/tlsserve"
)

// Live checks against the agent runtimes really installed on this machine.
// They call real models (a few cents of subscription quota each) and need
// logged-in agent CLIs, so they are skipped unless asked for:
//
//	MONOAGENT_LIVE_API_TESTS=1 go test ./internal/openaiapi -run Live -v -count=1
//
// The one for the auto model also calls Jev, so it needs TYPESAFE_API_KEY.

func liveGateway(t *testing.T, wrap func(ExecFunc) ExecFunc) (*Gateway, string, *harness) {
	t.Helper()
	g, secret, h, _ := liveGatewayDB(t, wrap)
	return g, secret, h
}

// liveGatewayDB is liveGateway that also returns the database it runs on.
func liveGatewayDB(t *testing.T, wrap func(ExecFunc) ExecFunc) (*Gateway, string, *harness, *sql.DB) {
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
	return g, secret, &harness{g: g}, db.DB
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
// reads the disk or runs a command, however the prompt asks. The model may
// still try a tool (monomind reports an attempt as a tool_activity start and
// then an end marked denied, "not in the tool list this exec call was given");
// what must never happen is a call that was not denied.
func TestLiveClaudeIsChatOnly(t *testing.T) {
	var attempts, ran atomic.Int32
	var leftovers atomic.Int32
	_, secret, h := liveGateway(t, func(inner ExecFunc) ExecFunc {
		return func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
			res, err := inner(ctx, opts, func(ev monomind.Event) {
				if ev.Type == monomind.EventToolActivity {
					switch {
					case ev.Denied:
					case ev.Phase == "start":
						attempts.Add(1)
					default: // an end that was not denied, or an event of an older monomind with no phase
						ran.Add(1)
					}
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
	if ran.Load() != 0 || leftovers.Load() != 0 {
		t.Fatalf("a chat-only turn ran %d native tools and left %d files: claude is not chat-only", ran.Load(), leftovers.Load())
	}
	var got completion
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	t.Logf("claude tried %d native tools, monomind refused every one, and answered %q (usage %+v)",
		attempts.Load(), strings.TrimSpace(got.Choices[0].Message.Content), got.Usage)
}

// One real chat per installed runtime. A runtime that is not signed in, is out of
// quota or whose own runner fails (502) is skipped, not failed.
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
			case http.StatusBadGateway:
				// The runtime's own runner failed: not signed in, or a CLI version
				// monomind does not drive (`monomind agent exec` fails the same way
				// outside the gateway). That says nothing about the gateway.
				t.Skipf("%s's runner failed on this machine: %d %s", rt, rec.Code, rec.Body)
			default:
				t.Errorf("%s: %d %s", rt, rec.Code, rec.Body)
			}
		})
	}
}

// With a Jev key and the surface on, a request for auto runs a model the list
// offered, picked by Jev or, when it cannot, by the rule. A chat-only policy
// keeps the choice to the chat-only models, so the turn is claude's.
func TestLiveAutoPicksAModel(t *testing.T) {
	_, secret, h, db := liveGatewayDB(t, nil)
	if os.Getenv("TYPESAFE_API_KEY") == "" {
		t.Skip("set TYPESAFE_API_KEY to run the live check of the auto model (it calls Jev)")
	}
	if err := jevconf.SetEnabled(db, "default", jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}
	policy := Policy{Max: ChatOnly}
	list := decodeModelList(t, h.serve(policy, http.MethodGet, "/v1/models", secret, ""))
	if len(list.Data) < 2 || list.Data[0].ID != autoModelID {
		t.Fatalf("auto is not offered first: %+v", list.Data)
	}
	offered := map[string]bool{}
	for _, m := range list.Data[1:] {
		offered[m.ID] = true
	}

	rec := h.serve(policy, http.MethodPost, "/v1/chat/completions", secret,
		`{"model":"auto","messages":[{"role":"user","content":"What is 17 times 23? Answer with the number only."}]}`)
	usableOrSkip(t, rec.Code, rec.Body.String())
	by, picked := rec.Header().Get("X-Monoagent-Auto"), rec.Header().Get("X-Monoagent-Model")
	if by != "jev" && by != "rule" {
		t.Errorf("X-Monoagent-Auto = %q, want jev or rule", by)
	}
	if !offered[picked] {
		t.Errorf("auto picked %q, which the list did not offer: %v", picked, offered)
	}
	var got completion
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Model != picked || len(got.Choices) == 0 || strings.TrimSpace(got.Choices[0].Message.Content) == "" {
		t.Errorf("the answer does not match the pick %q: %s", picked, rec.Body)
	}
	t.Logf("auto picked %s by %s among %d models", picked, by, len(offered))
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
