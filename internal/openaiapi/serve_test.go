package openaiapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/tlsserve"
)

// startServe runs Serve on a loopback port and returns its base address and
// a stop function that reports Serve's result.
func startServe(t *testing.T, h *harness, p Policy, tlsCfg *tls.Config) (addr string, stop func() error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.g.Serve(ctx, ln, p, tlsCfg) }()
	return ln.Addr().String(), func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("Serve did not stop after its context ended")
			return nil
		}
	}
}

func get(t *testing.T, client *http.Client, url, key string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestServeServesOnlyHealthAndV1(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	key := h.key(t, "default", "app", false)
	addr, stop := startServe(t, h, anyPolicy, nil)

	if code, body := get(t, http.DefaultClient, "http://"+addr+"/health", ""); code != 200 {
		t.Fatalf("/health: %d %s", code, body)
	} else {
		var got map[string]any
		if json.Unmarshal([]byte(body), &got) != nil || got["status"] != "ok" || got["version"] != "test" {
			t.Errorf("/health body: %s", body)
		}
	}
	if code, _ := get(t, http.DefaultClient, "http://"+addr+"/v1/models", ""); code != 401 {
		t.Errorf("/v1/models without a key: %d, want 401", code)
	}
	if code, _ := get(t, http.DefaultClient, "http://"+addr+"/v1/models", key); code != 200 {
		t.Errorf("/v1/models with a key: %d, want 200", code)
	}
	// Nothing else is served on this listener: no workflow, node or HIL routes.
	for _, path := range []string{"/workflows", "/nodes", "/hil", "/org-endpoint/ep_x"} {
		if code, _ := get(t, http.DefaultClient, "http://"+addr+path, key); code != 404 {
			t.Errorf("%s: %d, want 404 (not served here)", path, code)
		}
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve returned %v after its context ended", err)
	}
}

func TestServeOverTLS(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	key := h.key(t, "default", "app", false)

	cert, _, _, err := tlsserve.GenerateSelfSigned("serve test")
	if err != nil {
		t.Fatal(err)
	}
	addr, stop := startServe(t, h, Policy{Max: ChatOnly}, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	defer stop()

	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: 5 * time.Second}

	if code, _ := get(t, client, "https://"+addr+"/v1/models", key); code != 200 {
		t.Errorf("https /v1/models: %d, want 200", code)
	}
	// Plain HTTP to a TLS listener never reaches the handlers.
	plain := &http.Client{Timeout: 2 * time.Second}
	if resp, err := plain.Get("http://" + addr + "/health"); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Error("plain HTTP was answered by a TLS listener")
		}
	}
}

// Stopping the server must not leave an agent CLI running with nobody left to
// stop it: Serve returns only after the turns it cancelled have ended.
func TestServeWaitsForTheTurnsItCancelledOnShutdown(t *testing.T) {
	oldShutdown, oldDrain := shutdownGrace, drainGrace
	shutdownGrace, drainGrace = 50*time.Millisecond, 5*time.Second
	t.Cleanup(func() { shutdownGrace, drainGrace = oldShutdown, oldDrain })

	started := make(chan struct{})
	var ended atomic.Bool
	h := newHarness(t, func(ctx context.Context, _ monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		close(started)
		<-ctx.Done()                       // the connection closed: Exec starts to cancel
		time.Sleep(150 * time.Millisecond) // ...and needs a moment to kill the process group
		ended.Store(true)
		return &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	})
	key := h.key(t, "default", "app", false)
	addr, stop := startServe(t, h, anyPolicy, nil)

	go func() {
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/chat/completions", strings.NewReader(chatBody))
		req.Header.Set("Authorization", "Bearer "+key)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the runner")
	}

	if err := stop(); err != nil {
		t.Fatalf("Serve returned %v", err)
	}
	if !ended.Load() {
		t.Fatal("Serve returned while a turn it had cancelled was still running")
	}
}
