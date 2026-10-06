package accounttest

import (
	"os"
	"testing"

	"github.com/monoes/mono-agent/internal/testhome"
	"github.com/zalando/go-keyring"
)

// TestMain gives the test binary a throwaway HOME and an in-memory keyring, as
// internal/account's does, so that no test of the fixtures can touch the real
// ~/.monoagent or the real OS keychain (a regression that made a fixture use the
// default store would otherwise overwrite the developer's own session). It also
// drops the two file-keyring variables a developer's or a CI box's environment may
// export, which would put the production sealers on the file keyring.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Unsetenv("MONOAGENT_ALLOW_FILE_KEYRING")
	os.Unsetenv("MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE")
	testhome.Main(m)
}
