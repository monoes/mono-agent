//go:build !devaccount

package account_test

import (
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

// Spec D24: no environment variable redirects the session of a release build.
func TestDefaultBuildIgnoresMonoesBaseURL(t *testing.T) {
	t.Setenv("MONOES_BASE_URL", "http://127.0.0.1:3100")
	if got := account.Host(); got != account.HostURL {
		t.Fatalf("Host = %q: MONOES_BASE_URL redirected a default build", got)
	}
}
