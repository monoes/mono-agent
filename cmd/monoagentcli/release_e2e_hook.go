//go:build releasee2e

package main

import (
	"net/http"
	"os"

	"github.com/monoes/mono-agent/internal/release"
)

// Test-only (built with -tags releasee2e, never in a release): when
// MONOAGENT_E2E_LOOPBACK=host:port is set, every request the signed-update
// client makes is sent to that loopback server over plain HTTP with the path
// unchanged. The client's own https and host allow-list checks still run on
// the real URLs, so scripts/release-e2e-test.sh needs no network.
type loopbackTransport struct{ hostport string }

func (t loopbackTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host = "http", t.hostport
	return http.DefaultTransport.RoundTrip(r2)
}

func init() {
	hp := os.Getenv("MONOAGENT_E2E_LOOPBACK")
	if hp == "" {
		return
	}
	base := releaseClient
	releaseClient = func() *release.Client {
		c := base()
		c.HTTP = &http.Client{Transport: loopbackTransport{hp}}
		return c
	}
}
