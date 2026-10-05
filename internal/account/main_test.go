package account

import (
	"os"
	"testing"

	"github.com/monoes/mono-agent/internal/testhome"
	"github.com/zalando/go-keyring"
)

// TestMain gives the whole test binary (the internal and the external tests
// share it) a throwaway HOME and an in-memory keyring, so no test can touch the
// real ~/.monoagent or the real OS keychain. It also drops the two file-keyring
// variables a developer's or a CI box's environment may export: with the file
// keyring on, the production sealers no longer use the in-memory keyring above,
// and the quiet one cannot create a key. A test that wants the file keyring sets
// them itself (the child process of TestKeyringSealersInAFreshProcess does).
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Unsetenv("MONOAGENT_ALLOW_FILE_KEYRING")
	os.Unsetenv("MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE")
	testhome.Main(m)
}
