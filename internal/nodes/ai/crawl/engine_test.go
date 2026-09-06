package crawl

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateFetchURL_BlocksSSRF(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/admin",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data/", // cloud metadata endpoint
		"http://10.0.0.1/internal",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
		"http://0.0.0.0/",
		"file:///etc/passwd",     // non-http scheme
		"gopher://127.0.0.1:70/", // non-http scheme
	}
	for _, u := range blocked {
		if err := validateFetchURL(u); err == nil {
			t.Errorf("validateFetchURL(%q) = nil, want error", u)
		}
	}
}

func TestValidateFetchURL_AllowsPublicLiteral(t *testing.T) {
	// A public IP literal needs no DNS and must be permitted.
	if err := validateFetchURL("https://93.184.216.34/"); err != nil {
		t.Errorf("validateFetchURL(public IP) = %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// safeDialContext / DNS-rebinding regression tests (Finding 1)
//
// These tests prove that the SSRF guard cannot be bypassed by a DNS
// rebinding attack: a hostname that resolves to a public IP at validation
// time but to a private/blocked IP moments later, at actual connection time.
// The fix moves the authoritative check into the dialer itself, so the IP
// that is validated is the SAME IP that gets connected to, with no
// intervening (and separately re-resolvable) step.
// ---------------------------------------------------------------------------

func TestSafeDialContext_BlocksDNSRebinding(t *testing.T) {
	origResolver := dnsResolver
	origDial := rawDial
	t.Cleanup(func() {
		dnsResolver = origResolver
		rawDial = origDial
	})

	callCount := 0
	dnsResolver = func(_ context.Context, _ string) ([]net.IPAddr, error) {
		callCount++
		if callCount == 1 {
			// The earlier, validation-time lookup (as performed by
			// validateFetchURL or a redirect's CheckRedirect) sees a public
			// IP and would have allowed the request to proceed.
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		// DNS rebinding: the SAME hostname resolves to the cloud metadata
		// endpoint on a later lookup — e.g. the one the transport performs
		// right before actually connecting.
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}

	dialed := false
	rawDial = func(_ context.Context, _, _ string) (net.Conn, error) {
		dialed = true
		return nil, fmt.Errorf("must not be called for a blocked IP")
	}

	const host = "evil.example.com"

	// Simulate the earlier validation pass observing a public IP.
	if err := validateFetchURL("http://" + host + "/"); err != nil {
		t.Fatalf("validateFetchURL() = %v, want nil (first lookup returns a public IP)", err)
	}

	// The actual connection re-resolves independently and must see the
	// rebound private/metadata IP, refusing to connect to it.
	conn, err := safeDialContext(context.Background(), "tcp", host+":80")
	if err == nil {
		conn.Close()
		t.Fatal("safeDialContext() = nil error, want error for rebound private IP")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Errorf("safeDialContext() error = %v, want a non-public-address error", err)
	}
	if dialed {
		t.Error("safeDialContext() dialed the network despite the resolved IP being blocked")
	}
}

func TestSafeDialContext_DialsTheValidatedIP(t *testing.T) {
	origResolver := dnsResolver
	origDial := rawDial
	t.Cleanup(func() {
		dnsResolver = origResolver
		rawDial = origDial
	})

	dnsResolver = func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })

	var dialedAddr string
	rawDial = func(_ context.Context, _, address string) (net.Conn, error) {
		dialedAddr = address
		return clientConn, nil
	}

	conn, err := safeDialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatalf("safeDialContext() error = %v, want nil", err)
	}
	defer conn.Close()

	if dialedAddr != "93.184.216.34:443" {
		t.Errorf("safeDialContext() dialed %q, want the resolved IP pinned with the original port", dialedAddr)
	}
}

func TestSafeDialContext_BlocksLiteralPrivateIP(t *testing.T) {
	origDial := rawDial
	t.Cleanup(func() { rawDial = origDial })

	dialed := false
	rawDial = func(_ context.Context, _, _ string) (net.Conn, error) {
		dialed = true
		return nil, nil
	}

	if _, err := safeDialContext(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("safeDialContext() = nil error, want error for loopback literal")
	}
	if dialed {
		t.Error("safeDialContext() dialed a blocked literal IP address")
	}
}

// ---------------------------------------------------------------------------
// fetchStatic response body cap regression test (Finding 2)
// ---------------------------------------------------------------------------

func TestFetchStatic_CapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := bytes.Repeat([]byte("a"), 256)
		for i := 0; i < 100; i++ { // 25,600 bytes total, far above the lowered cap below.
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	origCap := maxHTMLBytes
	origTransport := httpClient.Transport
	maxHTMLBytes = 1024
	// This test targets the body-size cap (Finding 2), not the SSRF dialer
	// (Finding 1), so use a plain transport that can reach the loopback
	// httptest server (safeDialContext would otherwise correctly refuse it).
	httpClient.Transport = http.DefaultTransport
	t.Cleanup(func() {
		maxHTMLBytes = origCap
		httpClient.Transport = origTransport
	})

	_, err := fetchStatic(context.Background(), srv.URL, FetchOptions{Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("fetchStatic() = nil error, want error for a response exceeding the size cap")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("fetchStatic() error = %v, want an 'exceeds ... limit' error", err)
	}
}
