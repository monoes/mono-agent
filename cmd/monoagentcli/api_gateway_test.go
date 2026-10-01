package main

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
)

func newAPIRuntimeForTest(t *testing.T, f apiFlags) (*apiRuntime, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_CONTEXT_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_V1_ADDR", "")
	t.Setenv("MONOAGENT_API_MAX_CONCURRENT", "")
	t.Setenv("MONOAGENT_API_TURN_TIMEOUT", "")
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return newAPIRuntime(db.DB, f, func(string, ...any) {})
}

func TestComposeRoutesKeepsEveryRegistrar(t *testing.T) {
	mark := func(path string) func(*http.ServeMux) {
		return func(mux *http.ServeMux) {
			mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, path) })
		}
	}
	mux := http.NewServeMux()
	composeRoutes(mark("/org-endpoint/x"), nil, mark("/v1/probe"))(mux)
	for _, path := range []string{"/org-endpoint/x", "/v1/probe"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 || rec.Body.String() != path {
			t.Errorf("%s: %d %q — a registrar was lost", path, rec.Code, rec.Body)
		}
	}
}

func TestMainMountOnlyOnLoopback(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{})
	if err != nil {
		t.Fatal(err)
	}
	for addr, wantMount := range map[string]bool{
		"127.0.0.1:9322": true, "localhost:9322": true, "[::1]:9322": true,
		"0.0.0.0:9322": false, ":9322": false, "10.0.0.5:9322": false,
	} {
		if got := rt.mainMount(addr) != nil; got != wantMount {
			t.Errorf("mainMount(%q) mounted = %v, want %v", addr, got, wantMount)
		}
	}

	// A mounted main listener answers /v1 (401 without a key), and nothing is
	// mounted for an off-loopback bind.
	mux := http.NewServeMux()
	rt.mainMount("127.0.0.1:9322")(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/v1/models on the main listener: %d, want 401", rec.Code)
	}
}

func TestAPIRuntimeRejectsBadSettingsAsInvalidInput(t *testing.T) {
	for name, f := range map[string]apiFlags{
		"unknown confinement":         {confinement: "everything"},
		"unknown context confinement": {contextConfinement: "everything"},
		"negative max":                {maxConcurrent: -1},
	} {
		if _, err := newAPIRuntimeForTest(t, f); exitCode(err) != 3 {
			t.Errorf("%s: exit %d (%v), want 3", name, exitCode(err), err)
		}
	}

	t.Setenv("MONOAGENT_API_MAX_CONCURRENT", "lots")
	db, _ := storage.NewDatabase(filepath.Join(t.TempDir(), "x.db"))
	defer db.Close()
	_ = db.ApplyMigrations()
	if _, err := newAPIRuntime(db.DB, apiFlags{}, func(string, ...any) {}); exitCode(err) != 3 {
		t.Errorf("a bad MONOAGENT_API_MAX_CONCURRENT: exit %d (%v), want 3", exitCode(err), err)
	}

	t.Setenv("MONOAGENT_API_MAX_CONCURRENT", "")
	t.Setenv("MONOAGENT_API_CONTEXT_CONFINEMENT", "nope")
	if _, err := newAPIRuntime(db.DB, apiFlags{}, func(string, ...any) {}); exitCode(err) != 3 {
		t.Errorf("a bad MONOAGENT_API_CONTEXT_CONFINEMENT: exit %d (%v), want 3", exitCode(err), err)
	}
}

func TestAPIRuntimePolicyDefaultsAndOverride(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.policy("127.0.0.1:9322").String(); got != "any" {
		t.Errorf("loopback default: %s", got)
	}
	if got := rt.policy("0.0.0.0:9443").String(); got != "chat-only" {
		t.Errorf("off-loopback default: %s", got)
	}

	rt, err = newAPIRuntimeForTest(t, apiFlags{confinement: "sandboxed"})
	if err != nil {
		t.Fatal(err)
	}
	if rt.policy("127.0.0.1:9322").String() != "sandboxed" || rt.policy("0.0.0.0:9443").String() != "sandboxed" {
		t.Error("an explicit confinement must apply to every listener")
	}
}

// A key created with --context is held to chat-only unless the operator raises
// it with --context-confinement, and never above the listener's own policy.
func TestAPIRuntimeContextConfinement(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.policy("127.0.0.1:9322").ForContextKey().String(); got != "chat-only" {
		t.Errorf("a context key on a loopback listener by default: %s, want chat-only", got)
	}
	if got := rt.contextReport(); got != "chat-only" {
		t.Errorf("contextReport = %q, want chat-only", got)
	}

	rt, err = newAPIRuntimeForTest(t, apiFlags{contextConfinement: "sandboxed"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.policy("127.0.0.1:9322").ForContextKey().String(); got != "sandboxed" {
		t.Errorf("--context-confinement sandboxed on loopback: %s", got)
	}
	if got := rt.policy("0.0.0.0:9443").ForContextKey().String(); got != "chat-only" {
		t.Errorf("a chat-only network listener must stay chat-only for a context key: %s", got)
	}
	if got := rt.contextReport(); got != "sandboxed" {
		t.Errorf("contextReport = %q, want sandboxed", got)
	}
}

func TestStartV1ServesOnlyV1AndStopsWithItsContext(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{v1Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addr, err := rt.startV1(ctx)
	if err != nil || addr == "" || strings.HasSuffix(addr, ":0") {
		t.Fatalf("startV1 = %q, %v; want the bound address", addr, err)
	}

	status := func(path string) int {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := status("/health"); got != 200 {
		t.Errorf("/health: %d", got)
	}
	if got := status("/v1/models"); got != 401 {
		t.Errorf("/v1/models without a key: %d, want 401", got)
	}
	if got := status("/workflows"); got != 404 {
		t.Errorf("/workflows on the dedicated listener: %d, want 404", got)
	}

	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := http.Get("http://" + addr + "/health"); err != nil {
			return // the listener is gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the dedicated listener kept serving after its context ended")
}

func TestStartV1IsANoOpWithoutAnAddress(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if addr, err := rt.startV1(context.Background()); addr != "" || err != nil {
		t.Fatalf("startV1 with no address = %q, %v", addr, err)
	}
}

func TestStartV1OffLoopbackNeedsATLSCertificate(t *testing.T) {
	// Off-loopback the listener is TLS only: with no certificate configured a
	// self-signed one is generated and cached, never plaintext.
	rt, err := newAPIRuntimeForTest(t, apiFlags{v1Addr: "0.0.0.0:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOAGENT_API_TLS_CERT", "")
	t.Setenv("MONOAGENT_API_TLS_KEY", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := rt.startV1(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port := addr[strings.LastIndex(addr, ":")+1:]
	client := &http.Client{Timeout: 3 * time.Second}
	if resp, err := client.Get("http://127.0.0.1:" + port + "/health"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatal("an off-loopback listener answered plain HTTP")
		}
	}
}

// The daemon's single ExtraRoutes slot carries both: losing the org receiver
// would break every automation role, and /v1 must never appear off-loopback.
func TestDaemonRoutesKeepTheOrgReceiverAndMountV1OnlyOnLoopback(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{})
	if err != nil {
		t.Fatal(err)
	}
	receiver := func(mux *http.ServeMux) {
		mux.HandleFunc("GET /org-endpoint/x", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "receiver") })
	}
	codeOf := func(addr, path string) int {
		mux := http.NewServeMux()
		daemonRoutes(receiver, rt, addr)(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}
	for _, addr := range []string{"127.0.0.1:9322", "0.0.0.0:9322"} {
		if got := codeOf(addr, "/org-endpoint/x"); got != http.StatusOK {
			t.Errorf("%s: the org receiver answers %d, want 200: it must not be lost", addr, got)
		}
	}
	if got := codeOf("127.0.0.1:9322", "/v1/models"); got != http.StatusUnauthorized {
		t.Errorf("loopback: /v1/models answers %d, want 401 (mounted, asks for a key)", got)
	}
	if got := codeOf("0.0.0.0:9322", "/v1/models"); got != http.StatusNotFound {
		t.Errorf("off-loopback: /v1/models answers %d, want 404 (never mounted)", got)
	}
}

func TestStartV1OffLoopbackServesHTTPSWithTheGeneratedCertificate(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{v1Addr: "0.0.0.0:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOAGENT_API_TLS_CERT", "")
	t.Setenv("MONOAGENT_API_TLS_KEY", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := rt.startV1(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port := addr[strings.LastIndex(addr, ":")+1:]
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // the certificate is self-signed
	for path, want := range map[string]int{"/health": 200, "/v1/models": 401, "/workflows": 404} {
		resp, err := client.Get("https://127.0.0.1:" + port + path)
		if err != nil {
			t.Fatalf("GET %s over TLS: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s over TLS: %d, want %d", path, resp.StatusCode, want)
		}
	}
}

func TestDrainWaitsForTheDedicatedListenerToStop(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{v1Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addr, err := rt.startV1(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan struct{})
	go func() { rt.drain(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("drain did not return after the listener's context ended")
	}
	if resp, err := http.Get("http://" + addr + "/health"); err == nil {
		resp.Body.Close()
		t.Error("the dedicated listener still answers after drain")
	}
}

func TestConfinementReportOnlyForListenersThatServeV1(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		addr      string
		dedicated bool
		want      string
	}{
		{"127.0.0.1:9322", false, "any"},
		{"0.0.0.0:9322", false, ""}, // an off-loopback main listener does not serve /v1
		{"0.0.0.0:9443", true, "chat-only"},
		{"", true, ""},
	} {
		if got := rt.confinementReport(c.addr, c.dedicated); got != c.want {
			t.Errorf("confinementReport(%q, dedicated=%v) = %q, want %q", c.addr, c.dedicated, got, c.want)
		}
	}
}
