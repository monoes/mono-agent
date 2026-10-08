package monomind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// useAccountGate installs gate as Exec's account gate for one test.
func useAccountGate(t *testing.T, gate func(context.Context) error) {
	t.Helper()
	SetAccountGate(gate)
	t.Cleanup(func() { SetAccountGate(nil) })
}

// TestExecGate: with account.Require as the gate (what cmd/monoagentcli installs),
// a locked account is refused with the typed error before the monomind binary is
// even started; every other state runs the turn.
func TestExecGate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	for name, c := range map[string]struct {
		set     func(t *testing.T)
		refused bool
	}{
		"strict, no guard installed": {func(t *testing.T) {
			account.InstallForTest(t, nil)
			account.SetEnforceFromForTest(t, time.Now().Add(-24*time.Hour))
			account.StrictForTest(t)
		}, true},
		"locked, not logged in": {func(t *testing.T) { accounttest.Install(t, accounttest.LockedNoLogin) }, true},
		"dormant":               {func(t *testing.T) { accounttest.Install(t, accounttest.Dormant) }, false},
	} {
		t.Run(name, func(t *testing.T) {
			c.set(t)
			useAccountGate(t, account.Require)
			started := filepath.Join(t.TempDir(), "started")
			bin := writeInlineFakeBin(t, `echo started > `+started+`
echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"the answer"}'
echo '{"v":1,"type":"done","exit_code":0}'
exit 0
`)

			res, err := Exec(context.Background(), ExecOptions{Bin: bin, Runtime: "codex", Prompt: "hi"}, nil)

			_, statErr := os.Stat(started)
			ran := statErr == nil
			if c.refused && (!account.IsLoginRequired(err) || res != nil || ran) {
				t.Fatalf("res %v, err %v, binary started %v: want nil, *account.LoginRequiredError and no process", res, err, ran)
			}
			if !c.refused && (err != nil || res == nil || res.ResultText != "the answer" || !ran) {
				t.Fatalf("an admitted turn: res %v, err %v, binary started %v", res, err, ran)
			}
		})
	}
}

// requireAccount: an installed gate's error comes back as it is; with none
// installed a test binary is let through (every older suite) and any other
// process is refused.
func TestRequireAccount(t *testing.T) {
	ctx := context.Background()
	if err := requireAccount(ctx); err != nil {
		t.Fatalf("no gate in a test binary: %v, want nil", err)
	}

	was := inTestBinary
	inTestBinary = func() bool { return false }
	t.Cleanup(func() { inTestBinary = was })
	if err := requireAccount(ctx); !errors.Is(err, ErrNoAccountGate) {
		t.Fatalf("no gate outside a test binary: %v, want ErrNoAccountGate", err)
	}

	boom := errors.New("boom")
	useAccountGate(t, func(context.Context) error { return boom })
	if err := requireAccount(ctx); err != boom {
		t.Fatalf("an installed gate: %v, want its own error", err)
	}
}
