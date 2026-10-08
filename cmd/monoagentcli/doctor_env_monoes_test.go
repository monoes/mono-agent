package main

import (
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/health"
)

// doctor reports the machine's monoes.me session in one core row, and it
// stays usable (exit 0, the row is not required) in every state.
func TestDoctorReportsTheMonoesAccount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   accounttest.Mode
		status health.Status
		fix    string
	}{
		{"signed in", accounttest.SignedIn, health.StatusOK, ""},
		{"locked", accounttest.LockedNoLogin, health.StatusFail, health.FixMonoesLogin},
		{"refused", accounttest.LockedRefused, health.StatusFail, health.FixMonoesLogin},
		{"dormant", accounttest.Dormant, health.StatusInfo, ""},
	} {
		accounttest.Install(t, tc.mode)
		out, err := runDoctor(t, t.TempDir(), "doctor", "--json", "--check", health.CheckMonoesAccount)
		if err != nil {
			t.Fatalf("%s: doctor: %v\n%s", tc.name, err, out)
		}
		row := resultByID(decodeDoctor(t, out), health.CheckMonoesAccount)
		fix := ""
		if row.Fix != nil {
			fix = row.Fix.ID
		}
		if row.Status != tc.status || fix != tc.fix || row.Group != health.GroupCore || row.Required {
			t.Errorf("%s: row %+v", tc.name, row)
		}
	}
}

func TestHealthAccountMapsTheStatus(t *testing.T) {
	date, until := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 6, 14, 2, 0, 0, time.UTC)
	got := healthAccount(account.Status{State: account.StateGrace, Reason: account.ReasonServerError, User: &account.User{Email: "a@b.c"},
		GraceUntil: until, EnforceFrom: date, Enforced: true})
	want := health.AccountInfo{State: "grace", Reason: "server_error", Email: "a@b.c", GraceUntil: until, EnforceFrom: date, Enforced: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := healthAccount(account.Status{State: account.StateLocked, Reason: account.ReasonNotLoggedIn}); got.Email != "" || got.State != "locked" {
		t.Errorf("no user: %+v", got)
	}
}
