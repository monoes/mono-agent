package daemonhb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A heartbeat carries the account verdict as the desktop and doctor read it: the
// empty keys are left out, `enforced` is always there (a warn-period "locked",
// which refuses nothing, differs from a real lock only by it), and with nothing
// to report there is no account key at all.
func TestHeartbeatCarriesTheAccount(t *testing.T) {
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	until := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, c := range []struct {
		account *AccountState
		want    string // "" means: no account key at all
	}{
		{&AccountState{State: "locked", Reason: "expired", ValidUntil: until, Enforced: true}, `"account":{"state":"locked","reason":"expired","valid_until":"2027-01-02T03:04:05Z","enforced":true}`},
		{&AccountState{State: "locked", Reason: "not_logged_in"}, `"account":{"state":"locked","reason":"not_logged_in","enforced":false}`},
		{&AccountState{State: "ok", Enforced: true}, `"account":{"state":"ok","enforced":true}`},
		{nil, ""},
	} {
		if err := Write(Heartbeat{PID: os.Getpid(), Account: c.account}); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(Path())
		if has := strings.Contains(string(raw), `"account"`); has != (c.want != "") || (c.want != "" && !strings.Contains(string(raw), c.want)) {
			t.Errorf("heartbeat = %s, want it to contain %q", raw, c.want)
		}
		hb, _ := Read()
		got := hb.Account
		if (got == nil) != (c.account == nil) ||
			(got != nil && (got.State != c.account.State || got.Reason != c.account.Reason || got.Enforced != c.account.Enforced || !got.ValidUntil.Equal(c.account.ValidUntil))) {
			t.Errorf("read back %+v, want %+v", got, c.account)
		}
	}
}
