package openaiapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sync"
	"time"
)

// limiter caps the number of turns running at once: every turn starts a
// monomind process and an agent CLI, so an unbounded gateway would exhaust
// the machine. Each running turn holds one numbered slot, and the number
// names the folder it works in.
type limiter struct{ free chan int }

func newLimiter(n int) *limiter {
	if n < 1 {
		n = 1
	}
	l := &limiter{free: make(chan int, n)}
	for i := range n {
		l.free <- i
	}
	return l
}

// tryAcquire takes a slot without waiting and returns its number, from 0 up
// to the limit minus one. release frees the slot and is safe to call more
// than once.
func (l *limiter) tryAcquire() (slot int, release func(), ok bool) {
	select {
	case slot = <-l.free:
		var once sync.Once
		return slot, func() { once.Do(func() { l.free <- slot }) }, true
	default:
		return 0, nil, false
	}
}

// decodeBody reads the request's JSON body into dst, refusing more than
// limit bytes.
func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, dst any) *apiError {
	return decodeBodyWith(w, r, limit, dst, nil)
}

// decodeBodyWith is decodeBody that answers a field of the wrong JSON type through
// typeError (the field's name and the Go type it wanted) instead of "not valid
// JSON". nil keeps decodeBody's answer. A body that is not an object at all has no
// field to name and stays "not valid JSON".
func decodeBodyWith(w http.ResponseWriter, r *http.Request, limit int64, dst any, typeError func(field string, want reflect.Type) *apiError) *apiError {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		var wrongType *json.UnmarshalTypeError
		switch {
		case errors.As(err, &tooBig):
			return errTooLarge(limit)
		case errors.Is(err, io.EOF):
			return errInvalid("invalid_json", "", "the request body is empty")
		case typeError != nil && errors.As(err, &wrongType) && wrongType.Field != "":
			return typeError(wrongType.Field, wrongType.Type)
		}
		return errInvalid("invalid_json", "", "the request body is not valid JSON")
	}
	// net/http notices that a client has gone only once the body has been read to
	// its end, and a decoder stops at the closing brace: the end of a chunked body
	// comes after it. Without this a client that leaves while its turn runs goes
	// unnoticed, and the turn runs for nobody until its timeout. What the limit
	// allows is read and dropped.
	_, _ = io.Copy(io.Discard, r.Body)
	return nil
}

// extendWriteDeadline lifts the server-wide write timeout for this response:
// a turn, or a stream, can run for minutes. Writers that don't support
// deadlines (httptest recorders) are left alone.
func extendWriteDeadline(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}
