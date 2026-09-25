package bottest

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
)

// MethodProvider is the part of a bot adapter call_bot_method uses
// (action.BotAdapter.GetMethodByName).
type MethodProvider interface {
	GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool)
}

// CallTimeout bounds each CallMethod invocation.
var CallTimeout = 2 * time.Minute

// CallMethod invokes a bot method the way the call_bot_method step does
// (internal/action/steps.go): the page is prepended to args. An unknown
// method fails the test.
func CallMethod(t testing.TB, adapter MethodProvider, page browser.PageInterface, method string, args ...interface{}) (interface{}, error) {
	t.Helper()
	fn, ok := adapter.GetMethodByName(method)
	if !ok {
		t.Fatalf("bottest: method %q not found on %T", method, adapter)
	}
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	full := append([]interface{}{page}, args...)
	return fn(ctx, full...)
}
