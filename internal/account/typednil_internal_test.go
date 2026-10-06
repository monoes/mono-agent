package account

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// errFunc and errMap are errors of kinds that can be nil but are not pointers.
type (
	errFunc func()
	errMap  map[string]string
)

func (errFunc) Error() string { return "errFunc" }
func (errMap) Error() string  { return "errMap" }

// isTypedNil must tell a nil pointer inside an error from every other error without
// panicking: reflect's IsNil panics on a kind that cannot be nil, and
// context.DeadlineExceeded, among others, is a struct.
func TestIsTypedNil(t *testing.T) {
	var nilFunc errFunc
	var nilMap errMap
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"a nil *TransientError", (*TransientError)(nil), true},
		{"a nil *RefusedError", (*RefusedError)(nil), true},
		{"a *TransientError", &TransientError{Reason: ReasonUnreachable}, false},
		{"a *RefusedError", &RefusedError{}, false},
		{"an errors.New error, a pointer", errors.New("x"), false},
		{"context.DeadlineExceeded, a struct", context.DeadlineExceeded, false},
		{"a typed nil that another error wraps", fmt.Errorf("w: %w", (*TransientError)(nil)), false},
		{"a nil func that is an error", nilFunc, false},
		{"a nil map that is an error", nilMap, false},
	}
	for _, c := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("isTypedNil(%s) panicked: %v", c.name, r)
				}
			}()
			if got := isTypedNil(c.err); got != c.want {
				t.Errorf("isTypedNil(%s) = %v, want %v", c.name, got, c.want)
			}
		}()
	}
}
