//go:build !windows

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrgRoleAccessArgs(t *testing.T) {
	cases := []struct {
		root, access, want string
	}{
		{"/p", "full", "org --project /p role set-access growth builder full --yes-i-understand"},
		{"/p", "scoped", "org --project /p role set-access growth builder scoped"},
		{"", "full", "org role set-access growth builder full --yes-i-understand"},
	}
	for _, c := range cases {
		if got := strings.Join(orgRoleAccessArgs(c.root, "growth", "builder", c.access), " "); got != c.want {
			t.Errorf("orgRoleAccessArgs(%q, %q) = %q, want %q", c.root, c.access, got, c.want)
		}
	}
}

func TestApp_SetOrgRoleAccess_GrantRevokeAndRefusal(t *testing.T) {
	bin, argsLog := chatFakeCLI(t,
		fakeChatReply{match: "builder full", stdout: `{"org":"growth","role":"builder","access":"full","message":"granted"}` + "\n"},
		fakeChatReply{match: "builder scoped", stdout: `{"org":"growth","role":"builder","access":"scoped","message":"revoked"}` + "\n"},
		fakeChatReply{match: "analyst full", code: 3, stderr: "refused\n",
			stdout: `{"error":"granting full access is human-only and this looks like an agent (CLAUDECODE is set); run it yourself from a terminal or the app","code":"invalid_input"}` + "\n"},
	)
	a, _ := newCLIChatApp(t, bin)
	if got := a.setOrgRoleAccess("/p", "growth", "builder", "full"); !strings.Contains(got, `"access":"full"`) {
		t.Errorf("grant = %s", got)
	}
	if got := a.setOrgRoleAccess("/p", "growth", "builder", "scoped"); !strings.Contains(got, `"access":"scoped"`) {
		t.Errorf("revoke = %s", got)
	}
	var r struct{ Error, Code string }
	if err := json.Unmarshal([]byte(a.setOrgRoleAccess("/p", "growth", "analyst", "full")), &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != "granting full access is human-only and this looks like an agent (CLAUDECODE is set); run it yourself from a terminal or the app" || r.Code != "invalid_input" {
		t.Errorf("refusal = %+v, want the CLI's text verbatim", r)
	}
	if got := a.setOrgRoleAccess("/p", "growth", "builder", "root"); !strings.Contains(got, `"error"`) {
		t.Errorf("bad access = %s, want an error", got)
	}
	want := []string{
		"--profile default --json org --project /p role set-access growth builder full --yes-i-understand",
		"--profile default --json org --project /p role set-access growth builder scoped",
		"--profile default --json org --project /p role set-access growth analyst full --yes-i-understand",
	}
	if got := readArgsLog(t, argsLog); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("argv = %q\nwant %q", got, want)
	}
}

// An invalid org exits 1 but its report on stdout is the answer: taint
// problems arrive in its "error".
func TestApp_OrgValidateReport_KeepsTheReportOnFailure(t *testing.T) {
	invalid := `{"v":1,"org":"growth","valid":false,"warnings":[],"error":"role builder has full access but reads untrusted input via scraper → analyst → builder"}`
	bin, argsLog := chatFakeCLI(t,
		fakeChatReply{match: "validate growth", code: 1, stderr: "org growth is invalid\n", stdout: invalid + "\n"},
		fakeChatReply{match: "validate ok", stdout: `{"v":1,"org":"ok","valid":true,"warnings":[],"output":"valid"}` + "\n"},
		fakeChatReply{match: "validate broken", code: 1, stderr: "monomind not found\n"},
	)
	a, _ := newCLIChatApp(t, bin)
	if got := a.orgValidateReport("/p", "growth"); got != invalid {
		t.Errorf("invalid report = %s", got)
	}
	if got := a.orgValidateReport("", "ok"); !strings.Contains(got, `"valid":true`) {
		t.Errorf("valid report = %s", got)
	}
	if got := a.orgValidateReport("", "broken"); !strings.Contains(got, `"error":"monomind not found"`) {
		t.Errorf("failed run = %s", got)
	}
	want := []string{
		"--profile default --json org --project /p validate growth",
		"--profile default --json org validate ok",
		"--profile default --json org validate broken",
	}
	if got := readArgsLog(t, argsLog); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("argv = %q\nwant %q", got, want)
	}
}
