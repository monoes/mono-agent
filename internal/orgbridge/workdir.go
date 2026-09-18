package orgbridge

import (
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// senderWorkdir is the directory a message's sender is confined to in
// monomind, which an automation role's run gets as org.workdir so its file
// nodes confine to it (C-46): a role must not reach, through an automation
// role, files its own file tools cannot. from is "<role>" (a role of org)
// or "<org>:<role>".
//
//   - A role of an org under root: that role's workdir.
//   - An org-qualified sender root cannot resolve (another profile folder, a
//     role the file no longer has): the profile root, the widest directory
//     an org here can be given by default.
//   - A bare sender that is no role of org (a person, the operator): no
//     confinement (ok false), as for any run a person starts.
func senderWorkdir(root, org, from string) (string, bool) {
	senderOrg, role := org, from
	qualified := false
	if i := strings.IndexByte(from, ':'); i > 0 {
		senderOrg, role, qualified = from[:i], from[i+1:], true
	}
	if root == "" {
		return "", false
	}
	if doc, err := orgdesign.Load(root, senderOrg); err == nil {
		if r, _ := doc.FindRole(role); r != nil {
			return orgdesign.RoleWorkdir(root, doc, role), true
		}
	}
	if qualified {
		return filepath.Clean(root), true
	}
	return "", false
}
