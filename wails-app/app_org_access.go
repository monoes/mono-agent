package main

import (
	"encoding/json"
	"fmt"
)

// ─────────────────────────────────────────────────────────────────────────────
// Full-access org roles (issue #205). A role with full access runs like a
// coder chat: any command, any file, no approval gate. Granting it is
// human-only: the Orgs tab calls OrgRoleSetAccess only after its confirm
// dialog, and the CLI refuses a grant from an agent context. Its state
// (active / suspended / unattended-blocked) comes from `org status`'s
// roles_access, and taint problems from `org validate`; both are the CLI's
// JSON as-is.
// ─────────────────────────────────────────────────────────────────────────────

// orgRoleAccessArgs builds `org role set-access`: access "full" grants
// (with --yes-i-understand, which the confirm dialog stands for) and
// "scoped" revokes.
func orgRoleAccessArgs(root, orgName, roleID, access string) []string {
	args := []string{"org"}
	if root != "" {
		args = append(args, "--project", root)
	}
	args = append(args, "role", "set-access", orgName, roleID, access)
	if access == "full" {
		args = append(args, "--yes-i-understand")
	}
	return args
}

// OrgRoleSetAccess grants ("full") or revokes ("scoped") a role's full
// access: {org, role, access, message}, or the CLI's refusal verbatim.
func (a *App) OrgRoleSetAccess(orgName, roleID, access string) string {
	return a.setOrgRoleAccess(a.orgProjectRoot(), orgName, roleID, access)
}

func (a *App) setOrgRoleAccess(root, orgName, roleID, access string) string {
	if access != "full" && access != "scoped" {
		return aiError(fmt.Errorf("access must be full or scoped, got %q", access))
	}
	return a.jsonResult(orgRoleAccessArgs(root, orgName, roleID, access)...)
}

// ValidateOrgReport returns `org validate`'s report ({valid, error?,
// warnings}), which includes full-access taint problems. The CLI exits 1 for
// an invalid org but the report on stdout is still the answer.
func (a *App) ValidateOrgReport(orgName string) string {
	return a.orgValidateReport(a.orgProjectRoot(), orgName)
}

func (a *App) orgValidateReport(root, orgName string) string {
	args := []string{"org"}
	if root != "" {
		args = append(args, "--project", root)
	}
	out, err := a.jsonCLI(append(args, "validate", orgName)...)
	var report struct {
		Valid *bool `json:"valid"`
	}
	if json.Unmarshal([]byte(lastLine(out)), &report) == nil && report.Valid != nil {
		return lastLine(out)
	}
	if err != nil {
		return aiError(err)
	}
	return aiError(fmt.Errorf("org validate %s: unexpected output: %q", orgName, out))
}

// OrgSectionsRuntimes returns `org sections-runtimes`: {source, runtimes:
// [{runtime, status: refused|unverified, reason}]}, monomind's own reasons
// for the runtime picker of a sections org.
func (a *App) OrgSectionsRuntimes() string {
	return a.jsonResult("org", "sections-runtimes")
}
