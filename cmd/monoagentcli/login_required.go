package main

import (
	"errors"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library"
)

// loginRequiredError is exit 4 with "login_required": true and the account's state
// in --json, so a caller can tell "log in" from a connection failure.
type loginRequiredError struct {
	*cliError
	status account.Status
}

func (e loginRequiredError) Unwrap() error { return e.cliError }

// JSONErrorFields adds login_required, the code and the account's state and reason
// to the error document, the same keys as (*account.LoginRequiredError).JSONErrorFields:
// an error that main prints, which no command wrapper has classified, carries its code
// here.
func (e loginRequiredError) JSONErrorFields() map[string]any {
	return map[string]any{"login_required": true, "code": "auth_or_connection",
		"account": map[string]any{"state": string(e.status.State), "reason": string(e.status.Reason)}}
}

// newLoginRequiredError is the error of a gate that refused st: exit 4, and the
// message of account.LoginRequiredError, whose first line is
// "Log in to monoes.me first: monoagentcli account login".
func newLoginRequiredError(st account.Status) error {
	return loginRequiredError{&cliError{code: 4, msg: (&account.LoginRequiredError{Status: st}).Error()}, st}
}

// libraryLoginRequired is the library's own "log in first". Its text names
// `library login`, which still works: it is an alias of `account login`.
func libraryLoginRequired() error {
	return loginRequiredError{&cliError{code: 4, msg: library.ErrNotLoggedIn.Error()}, account.CurrentStatus()}
}

// isLoginRequired reports whether err means "log in to monoes.me first".
func isLoginRequired(err error) bool {
	var lr loginRequiredError
	return errors.Is(err, library.ErrNotLoggedIn) || errors.As(err, &lr) || account.IsLoginRequired(err)
}
