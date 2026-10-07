package account_test

import (
	"errors"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

// A typed nil is an error that holds a nil pointer: `var e *TransientError;
// return e` in a function that returns error. Whoever returns one has a bug, but
// whoever prints, wraps or unwraps it must not take the process down, so the two
// refresh errors answer on a nil receiver. RefusedError has no Unwrap.
func TestTheRefreshErrorsAnswerOnANilReceiver(t *testing.T) {
	var refused *account.RefusedError
	var transient *account.TransientError
	check := func(what string, fn func() string) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("%s panicked on a nil receiver: %v", what, r)
			}
		}()
		if got := fn(); got != "" {
			t.Errorf("%s = %q on a nil receiver, want an empty string", what, got)
		}
	}
	check("(*RefusedError).Error", func() string { return refused.Error() })
	check("(*TransientError).Error", func() string { return transient.Error() })
	check("(*TransientError).Unwrap", func() string {
		if err := transient.Unwrap(); err != nil {
			return "an error: " + err.Error()
		}
		return ""
	})
}

// errors.As walks an error's Unwrap chain, so a typed-nil *TransientError inside
// an error must not panic it on the way to a target that it is not.
func TestAnErrorsAsWalkOverATypedNilTransientErrorDoesNotPanic(t *testing.T) {
	var err error = (*account.TransientError)(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("errors.As panicked: %v", r)
		}
	}()
	var refused *account.RefusedError
	if errors.As(err, &refused) {
		t.Fatal("errors.As found a *RefusedError inside a typed-nil *TransientError")
	}
	if errors.Is(err, errors.New("other")) {
		t.Fatal("errors.Is matched a typed-nil *TransientError with an unrelated error")
	}
}
