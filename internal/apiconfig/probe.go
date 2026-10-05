package apiconfig

import (
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProbeFunc asks a listener at base ("http://127.0.0.1:9322") whether GET /health answers 200
// and, when wantV1, whether GET /v1/models sent without a key is refused with 401.
type ProbeFunc func(base string, wantV1 bool) (reachable, v1Answers bool)

// ProbeSchemes is the order to try a listener's schemes in: the one it more
// likely speaks first. A listener off loopback is TLS only, and a plaintext
// request to it would put a handshake error in its log on every `api status`.
// Both are always tried: the certificate may be set only in the server's own
// environment, which makes a loopback listener speak TLS too.
func ProbeSchemes(loopback bool) []string {
	if loopback {
		return []string{"http://", "https://"}
	}
	return []string{"https://", "http://"}
}

// ProbeAddr probes a listener at addr without knowing whether it speaks TLS (see ProbeSchemes),
// and, when wantV1, whether it answers /v1. It says which scheme answered ("http" or "https"),
// and none when nothing did. probe nil is ProbeListener.
func ProbeAddr(addr string, loopback, wantV1 bool, probe ProbeFunc) (scheme string, reachable, v1Answers bool) {
	if probe == nil {
		probe = ProbeListener
	}
	for _, s := range ProbeSchemes(loopback) {
		if reachable, v1Answers = probe(s+addr, wantV1); reachable {
			return strings.TrimSuffix(s, "://"), reachable, v1Answers
		}
	}
	return "", false, false
}

// ProbeListener asks a listener, with a 2 s timeout each, whether GET /health
// answers 200 and, when wantV1, whether GET /v1/models sent without a key is
// refused with 401. Only the API answers 401 there; a server that predates it
// answers 404. No request carries a secret, so the self-signed certificate of
// the dedicated listener is not verified.
func ProbeListener(base string, wantV1 bool) (reachable, v1Answers bool) {
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // no secret is sent
		// What answers is whatever is at the address: it is not followed anywhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	status := func(path string) int {
		resp, err := client.Get(base + path)
		if err != nil {
			return 0
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	if status("/health") != http.StatusOK {
		return false, false
	}
	return true, wantV1 && status("/v1/models") == http.StatusUnauthorized
}
