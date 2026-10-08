package accounttest

import (
	"os"
	"strings"
	"testing"
)

// The test binary runs on a throwaway HOME and USERPROFILE (main_test.go), so a
// regression that made a fixture use the default store would write there and not
// into the developer's own ~/.monoagent, and the file-keyring switches that a
// developer's or a CI box's shell may export are gone.
func TestTheTestBinaryHasAThrowawayHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || !strings.Contains(home, "monoagent-testhome-") {
		t.Fatalf("HOME = %q (%v), want the throwaway test home", home, err)
	}
	if profile := os.Getenv("USERPROFILE"); profile != home {
		t.Fatalf("USERPROFILE = %q, want %q", profile, home)
	}
	for _, name := range []string{"MONOAGENT_ALLOW_FILE_KEYRING", "MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE"} {
		if _, set := os.LookupEnv(name); set {
			t.Fatalf("%s is set in the test binary", name)
		}
	}
}
