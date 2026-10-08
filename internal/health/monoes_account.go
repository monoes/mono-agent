package health

import (
	"context"
	"fmt"
	"time"
)

// The monoes.me sign-in has its own row in the core group (spec D25): the
// accounts group is about the logins of other platforms.
const (
	CheckMonoesAccount = "core.monoes_account"
	FixMonoesLogin     = "core.monoes_account.login"
)

// AccountInfo is the monoes.me session as this machine sees it: an
// account.Status without the dependency. Reading it is local, so the check
// changes nothing and uses no network.
type AccountInfo struct {
	State       string // ok, grace or locked; "" when unknown
	Reason      string
	Email       string
	GraceUntil  time.Time
	EnforceFrom time.Time // zero while enforcement is dormant
	Enforced    bool
}

func monoesAccountChecks() []Check {
	return []Check{{ID: CheckMonoesAccount, Group: GroupCore, Title: "monoes.me account",
		Features: []string{"every command except version, ref, update, doctor, setup and account"}, Run: checkMonoesAccount}}
}

// The fix is manual: signing in needs a browser or an emailed code, so
// `doctor --fix` and `setup` only name the command.
func monoesAccountFixes() []Fix {
	manual := func(context.Context, *Env, func(string)) error { return fmt.Errorf("this needs to be done by hand") }
	return []Fix{{FixInfo: FixInfo{ID: FixMonoesLogin, Label: "Sign in to monoes.me", Safety: SafetyManual,
		Command: "monoagentcli account login"}, Apply: manual}}
}

// lockedWhy says why a login is locked, by the reason the session reports.
var lockedWhy = map[string]string{
	"not_logged_in":  "not signed in to monoes.me",
	"expired":        "the login expired: monoes.me has not been reachable for more than 24 hours",
	"refused":        "monoes.me ended this login",
	"clock_rollback": "the system clock is earlier than the last time this login was used",
	"clock_skew":     "the system clock is more than 5 minutes behind monoes.me",
	"key_unknown":    "this build does not know the key monoes.me signs logins with",
	"invalid":        "the stored login is not valid",
	"unconfirmed":    "monoes.me may have received a refresh whose answer never arrived, so this machine stopped using its saved login",
}

// checkMonoesAccount: ok when signed in; warn when the login works offline
// (grace) or will be required from a date; fail when locked. While
// enforcement is dormant (no date) it never warns or fails, and says so.
func checkMonoesAccount(ctx context.Context, env *Env) Result {
	if env.MonoesAccount == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	a := env.MonoesAccount(ctx)
	dormant := a.EnforceFrom.IsZero()
	why := lockedWhy[a.Reason]
	if why == "" {
		why = "not signed in to monoes.me"
	}
	switch a.State {
	case "ok":
		if a.Email != "" {
			return Result{Status: StatusOK, Summary: "signed in as " + a.Email}
		}
		return Result{Status: StatusOK, Summary: "signed in"}
	case "grace":
		res := Result{Status: StatusWarn, Summary: graceSummary(a)}
		switch {
		case dormant:
			res.Status = StatusInfo
		case a.Reason == "unconfirmed": // this machine cannot renew the login: the user has to sign in again (A24)
			res.FixID = FixMonoesLogin
		}
		return res
	case "locked":
		switch {
		case dormant:
			res := Result{Status: StatusInfo, Summary: "not signed in; a monoes.me sign-in is not required yet"}
			if a.Reason != "not_logged_in" {
				res.Detail = why
			}
			return res
		case !a.Enforced:
			return Result{Status: StatusWarn, FixID: FixMonoesLogin, Detail: why,
				Summary: "a monoes.me login will be required from " + a.EnforceFrom.Local().Format("2006-01-02")}
		case a.Reason == "key_unknown":
			return Result{Status: StatusFail, Summary: why, FixID: FixUpdate}
		}
		return Result{Status: StatusFail, Summary: why, FixID: FixMonoesLogin}
	}
	return Result{Status: StatusSkip, Summary: "not available"}
}

// graceSummary says why the login was not renewed, and until when it works.
func graceSummary(a AccountInfo) string {
	until := a.GraceUntil.Local().Format("2006-01-02 15:04")
	switch a.Reason {
	case "keyring_unavailable":
		return "the key store cannot be opened, so the login is not renewed; it works offline until " + until
	case "server_error":
		return "monoes.me answered with an error; this login works offline until " + until
	case "unconfirmed":
		return "monoes.me may have received a refresh whose answer never arrived, so this machine stopped using its saved login; it works until " + until + ". Sign in again"
	}
	return "monoes.me is unreachable; this login works offline until " + until
}
