package connections

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A validator's non-200 answer keeps its status code, so doctor can offer a
// token refresh only when the service rejected the credentials.
func TestValidatorStatusErrors(t *testing.T) {
	orig := http.DefaultTransport
	defer func() { http.DefaultTransport = orig }()
	for _, tc := range []struct {
		code     int
		rejected bool
	}{{401, true}, {403, true}, {500, false}, {429, false}} {
		http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}, Request: r}, nil
		})
		for _, platform := range []string{"github", "notion", "telegram"} {
			conn := &Connection{Platform: platform, Data: map[string]interface{}{
				"token": "tok-123456", "access_token": "tok-123456", "bot_token": "tok-123456"}}
			_, err := ValidateConnection(context.Background(), conn)
			if HTTPStatus(err) != tc.code || CredentialsRejected(err) != tc.rejected {
				t.Errorf("%s %d: err %v (status %d, rejected %v)", platform, tc.code, err, HTTPStatus(err), CredentialsRejected(err))
			}
		}
	}
	if err := (&StatusError{Code: 500}); err.Error() != "unexpected status 500" {
		t.Errorf("message: %q", err.Error())
	}
}
