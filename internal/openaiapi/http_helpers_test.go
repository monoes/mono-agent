package openaiapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
)

// do runs a request through the gateway mounted with policy p.
func (h *harness) do(p Policy, r *http.Request) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.g.Mount(mux, p)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

// serve builds a request (JSON body when body is not empty, Bearer secret
// when secret is not empty) and runs it with do.
func (h *harness) serve(p Policy, method, path, secret, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	return h.do(p, r)
}

type httpRecorder = httptest.ResponseRecorder
