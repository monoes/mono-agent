package health

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Every state the session can be in, with enforcement dormant, in its warn
// period and on: what the row says and what it offers.
func TestMonoesAccountCheck(t *testing.T) {
	date := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 6, 14, 2, 0, 0, time.UTC)
	on := func(a AccountInfo) AccountInfo { a.EnforceFrom, a.Enforced = date, true; return a }
	warn := func(a AccountInfo) AccountInfo { a.EnforceFrom = date; return a }
	cases := []struct {
		name    string
		in      AccountInfo
		status  Status
		fix     string
		summary string
	}{
		{"signed in", on(AccountInfo{State: "ok", Email: "a@b.c"}), StatusOK, "", "signed in as a@b.c"},
		{"signed in, no address", on(AccountInfo{State: "ok"}), StatusOK, "", "signed in"},
		{"offline", on(AccountInfo{State: "grace", Reason: "unreachable", GraceUntil: until}), StatusWarn, "", "monoes.me is unreachable; this login works offline until " + until.Local().Format("2006-01-02 15:04")},
		{"server error", on(AccountInfo{State: "grace", Reason: "server_error", GraceUntil: until}), StatusWarn, "", "answered with an error"},
		{"key store", on(AccountInfo{State: "grace", Reason: "keyring_unavailable", GraceUntil: until}), StatusWarn, "", "key store cannot be opened"},
		{"unconfirmed, still working", on(AccountInfo{State: "grace", Reason: "unconfirmed", GraceUntil: until}), StatusWarn, FixMonoesLogin, "answer never arrived"},
		{"not signed in", on(AccountInfo{State: "locked", Reason: "not_logged_in"}), StatusFail, FixMonoesLogin, "not signed in to monoes.me"},
		{"refused", on(AccountInfo{State: "locked", Reason: "refused"}), StatusFail, FixMonoesLogin, "monoes.me ended this login"},
		{"expired", on(AccountInfo{State: "locked", Reason: "expired"}), StatusFail, FixMonoesLogin, "more than 24 hours"},
		{"unknown key", on(AccountInfo{State: "locked", Reason: "key_unknown"}), StatusFail, FixUpdate, "does not know the key"},
		{"unconfirmed, ended", on(AccountInfo{State: "locked", Reason: "unconfirmed"}), StatusFail, FixMonoesLogin, "answer never arrived"},
		{"warn period", warn(AccountInfo{State: "locked", Reason: "not_logged_in"}), StatusWarn, FixMonoesLogin, "required from " + date.Local().Format("2006-01-02")},
		{"dormant, not signed in", AccountInfo{State: "locked", Reason: "not_logged_in"}, StatusInfo, "", "not required yet"},
		{"dormant, refused", AccountInfo{State: "locked", Reason: "refused"}, StatusInfo, "", "not required yet"},
		{"dormant, offline", AccountInfo{State: "grace", Reason: "unreachable", GraceUntil: until}, StatusInfo, "", "works offline"},
		{"dormant, unconfirmed", AccountInfo{State: "grace", Reason: "unconfirmed", GraceUntil: until}, StatusInfo, "", "answer never arrived"},
		{"dormant, signed in", AccountInfo{State: "ok", Email: "a@b.c"}, StatusOK, "", "signed in as"},
		{"unknown state", AccountInfo{}, StatusSkip, "", "not available"},
	}
	for _, tc := range cases {
		res := checkMonoesAccount(context.Background(), &Env{MonoesAccount: func(context.Context) AccountInfo { return tc.in }})
		if res.Status != tc.status || res.FixID != tc.fix || !strings.Contains(res.Summary, tc.summary) {
			t.Errorf("%s: %q %q fix %q, want %q fix %q containing %q", tc.name, res.Status, res.Summary, res.FixID, tc.status, tc.fix, tc.summary)
		}
	}
	if res := checkMonoesAccount(context.Background(), &Env{}); res.Status != StatusSkip {
		t.Errorf("no hook: %q, want skip", res.Status)
	}
}

// The registered row is a core row (never a required one), its fix is
// manual and names the command, and it sits among the core rows so the
// printed report keeps one core heading.
func TestMonoesAccountRowIsRegisteredInTheCoreGroup(t *testing.T) {
	reg := Default()
	rep := reg.Run(context.Background(), &Env{MonoesAccount: func(context.Context) AccountInfo {
		return AccountInfo{State: "locked", Reason: "not_logged_in", Enforced: true, EnforceFrom: time.Now().Add(-time.Hour)}
	}}, Options{IDs: []string{CheckMonoesAccount}})
	if len(rep.Results) != 1 {
		t.Fatalf("%d rows", len(rep.Results))
	}
	r := rep.Results[0]
	if r.ID != CheckMonoesAccount || r.Group != GroupCore || r.Required || r.Fix == nil ||
		r.Fix.ID != FixMonoesLogin || r.Fix.Safety != SafetyManual || r.Fix.Command != "monoagentcli account login" {
		t.Fatalf("row %+v fix %+v", r, r.Fix)
	}
	first, n := -1, 0
	for i, c := range reg.Checks() {
		if c.Group == GroupCore {
			if first < 0 {
				first = i
			}
			n++
		}
	}
	for i := first; i < first+n; i++ {
		if reg.Checks()[i].Group != GroupCore {
			t.Fatalf("the core checks are split: %s sits among them", reg.Checks()[i].ID)
		}
	}
}
