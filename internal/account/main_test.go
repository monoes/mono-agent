package account

import (
	"testing"

	"github.com/monoes/mono-agent/internal/testhome"
	"github.com/zalando/go-keyring"
)

// TestMain gives the whole test binary (the internal and the external tests
// share it) a throwaway HOME and an in-memory keyring, so no test can touch the
// real ~/.monoagent or the real OS keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	testhome.Main(m)
}
