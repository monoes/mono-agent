package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOrgToolsCannotGrantFullAccess: full access for an org role is
// human-only (monomind#365), so no assistant tool may take a role policy,
// an access level or an ack — a model-authored grant must not be possible
// even as an unsigned one.
func TestOrgToolsCannotGrantFullAccess(t *testing.T) {
	roleWriters := map[string]bool{"create_org": true, "add_org_role": true, "update_org_role": true, "set_role_reports_to": true, "remove_org_role": true}
	seen := 0
	mt := NewMonoagentTools(nil, "")
	for _, d := range mt.ToolDefs() {
		if !roleWriters[d.Function.Name] {
			continue
		}
		seen++
		b, _ := json.Marshal(d.Function.Parameters)
		for _, banned := range []string{`"policy"`, `"access"`, `"access_ack"`} {
			if strings.Contains(string(b), banned) {
				t.Errorf("tool %s exposes %s", d.Function.Name, banned)
			}
		}
	}
	if seen != len(roleWriters) {
		t.Errorf("found %d of the %d role-writing tools; update this list if one was renamed", seen, len(roleWriters))
	}
}
