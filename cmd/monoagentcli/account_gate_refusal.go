package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
)

// gateRefusal is what a refused command fails with: exit 4 and the
// login_required fields of newLoginRequiredError, under the text of
// account.LoginRequiredError (the fixed first line and, for most reasons, a
// second one), so the gate says what the other doors say.
type gateRefusal struct {
	error
	msg string
}

func (e *gateRefusal) Error() string { return e.msg }
func (e *gateRefusal) Unwrap() error { return e.error }

func newGateRefusal(st account.Status) error {
	return &gateRefusal{error: newLoginRequiredError(st), msg: (&account.LoginRequiredError{Status: st}).Error()}
}

// gateCommand is the CLI gate. It returns nil to let the command run and the
// refusal to fail it. Before it judges, a gated or serving command renews the
// session when that is due (an offline process pays one 2-second attempt a
// minute at most). g is nil when no guard could be built; the verdict is then
// account.CurrentStatus's, which is locked once enforcement is on.
func gateCommand(ctx context.Context, root *cobra.Command, args []string, g *account.Guard, stderr io.Writer) error {
	class := invocationClass(root, args)
	if class == classOpen {
		return nil
	}
	st := gateStatus(ctx, g)
	switch {
	case st.Allowed():
		announce(stderr, st)
	case class == classServe:
		// It starts anyway and refuses the work itself: say why, once, on
		// stderr (an MCP server's stdout is its protocol).
		fmt.Fprintln(stderr, newGateRefusal(st))
	default:
		return newGateRefusal(st)
	}
	return nil
}

// gateStatus renews the session when that is due and reads the verdict. EnsureFresh may block after
// a Ctrl-C while a refresh grant is in flight, for the grant and then for the key-store write of the
// new refresh token (about 20 seconds, 30 at the worst): the guard never abandons a grant it has
// sent (A20), because the answer holds the only copy of the refresh token that replaces the one
// monoes.me has already rotated. The signals stay caught until the command ends, so a second
// Ctrl-C does not shorten the wait; only SIGKILL does.
func gateStatus(ctx context.Context, g *account.Guard) account.Status {
	if g == nil {
		return account.CurrentStatus()
	}
	_, _ = g.EnsureFresh(ctx) // refreshes when due, never when dormant; the verdict is read back
	return g.Status()
}

// announce is the one line an allowed command owes the user, on stderr only:
// the grace line when the login works offline (or, once a refresh whose answer
// never arrived has cost this machine its refresh token, A24, the line that says
// to sign in again), the warning when a login will be required from a date.
// While enforcement is dormant (no date) it says nothing.
func announce(w io.Writer, st account.Status) {
	if st.EnforceFrom.IsZero() {
		return
	}
	switch st.State {
	case account.StateGrace:
		if st.Reason == account.ReasonUnconfirmed { // monoes.me is not the problem
			fmt.Fprintf(w, "This login can no longer be renewed on this machine and works until %s. Sign in again: monoagentcli account login\n",
				st.GraceUntil.Local().Format(time.RFC3339))
			return
		}
		fmt.Fprintf(w, "monoes.me is unreachable; this login works offline until %s\n", st.GraceUntil.Local().Format(time.RFC3339))
	case account.StateLocked: // allowed only before the date
		fmt.Fprintf(w, "A monoes.me login will be required from %s: monoagentcli account login\n", st.EnforceFrom.Local().Format("2006-01-02"))
	}
}
