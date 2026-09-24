package connections

import (
	"errors"
	"fmt"
	"net/http"
)

// StatusError is the HTTP status a validator got instead of success. It
// lets a caller tell a rejected credential (401/403) from a service fault
// (5xx, 429) without parsing the message.
type StatusError struct {
	Code int
	Body string // response body, when the validator keeps it
}

func (e *StatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("unexpected status %d: %s", e.Code, e.Body)
	}
	return fmt.Sprintf("unexpected status %d", e.Code)
}

// HTTPStatus returns the status code carried by err, or 0 when it carries
// none.
func HTTPStatus(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

// CredentialsRejected reports whether err is the service refusing the
// credentials: HTTP 401 or 403.
func CredentialsRejected(err error) bool {
	code := HTTPStatus(err)
	return code == http.StatusUnauthorized || code == http.StatusForbidden
}
