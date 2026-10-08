package account

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestEnforceDateHelper is the child of the tests below: it reports the date this build starts with.
func TestEnforceDateHelper(t *testing.T) {
	if os.Getenv("ACCOUNT_OVERRIDE_HELPER") == "1" {
		fmt.Println("date:", EnforceDate().UTC().Format(time.RFC3339))
	}
}

// startsWith runs this test binary again with the variable set to value ("" leaves it unset) and
// returns what the child reports and whether it started at all.
func startsWith(t *testing.T, value string) (out string, ok bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestEnforceDateHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "ACCOUNT_OVERRIDE_HELPER=1", "MONOAGENT_DEV_ENFORCE_FROM="+value)
	b, err := cmd.CombinedOutput()
	return string(b), err == nil
}

// date is the date out reports, "" when it reports none.
func date(out string) string {
	if _, after, found := strings.Cut(out, "date: "); found {
		return strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])
	}
	return ""
}
