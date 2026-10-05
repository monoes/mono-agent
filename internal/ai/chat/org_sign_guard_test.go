package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// Signing an org is the operator's alone (monomind v2.24.1: an org role
// can't sign, and ~/.monomind/orgrt-operator is hidden from every role). A
// tool call made from inside a role reaches mono-agent only through these
// tools, so none of them may sign, check signatures, or name the operator
// dir: reload_org and the org writers leave a changed org unsigned for the
// operator to review.
func TestChatToolsHaveNoSigningOrOperatorDirSurface(t *testing.T) {
	mt := NewMonoagentTools(nil, "")
	for _, d := range mt.ToolDefs() {
		b, _ := json.Marshal(d)
		all := strings.ToLower(string(b))
		for _, banned := range []string{"orgrt-operator", "operator dir", "operator key", "org sign", "expect-hash"} {
			if strings.Contains(all, banned) {
				t.Errorf("tool %s mentions %q", d.Function.Name, banned)
			}
		}
		n := strings.ToLower(d.Function.Name)
		if strings.Contains(n, "sign") || strings.Contains(n, "operator") {
			t.Errorf("tool %s is a signing/operator surface", d.Function.Name)
		}
	}
}
