package library

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A redirect from https to http is refused for a GET too, so a same-host downgrade cannot carry
// the Authorization header in the clear.
func TestHTTPSToHTTPRedirectIsRefused(t *testing.T) {
	var seenAuth atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			seenAuth.Add(1)
		}
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/x", http.StatusFound)
	}))
	defer secure.Close()

	hc := secure.Client()
	hc.CheckRedirect = noRedirectWithABody
	req, _ := http.NewRequest(http.MethodGet, secure.URL+"/x", nil)
	req.Header.Set("Authorization", "Bearer at-secret")
	resp, err := hc.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "https to http") {
		t.Fatalf("err = %v, want the downgrade refused", err)
	}
	if seenAuth.Load() != 0 {
		t.Error("the Authorization header reached the plain server")
	}
}

// An https to https redirect of a GET still works.
func TestHTTPSToHTTPSRedirectIsFollowed(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	hc := target.Client()
	hc.CheckRedirect = noRedirectWithABody
	src := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer src.Close()
	// Both test servers use per-server certificates; trust the target's pool for the whole chain.
	hc.Transport = &http.Transport{TLSClientConfig: target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
	hc.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true
	resp, err := hc.Get(src.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
