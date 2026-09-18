// Package orggrant is the enforcement side of org → automation grants and
// automation-role endpoints (docs/plans/2026-09-15-org-workflow-unification.md
// §6.2, §7.1, §7.2). The org JSON carries only display copies; every
// decision about what a role may call reads the org_grants / org_endpoints
// rows kept here. Rows are created only by explicit commands (CLI, GUI, chat
// tool); Reconcile only strips JSON and revokes rows (C-3).
package orggrant

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
	"time"
)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func randomBase32(n int) string {
	b := make([]byte, (n*5+7)/8)
	if _, err := rand.Read(b); err != nil {
		panic("orggrant: crypto/rand failed: " + err.Error())
	}
	return strings.ToLower(idEncoding.EncodeToString(b))[:n]
}

// NewGrantID returns "grt_" + 22 base32 chars. Grant ids select a scope;
// they are not secrets (C-4).
func NewGrantID() string { return "grt_" + randomBase32(22) }

// NewEndpointID returns "ep_" + 26 base32 chars (130 random bits). Endpoint
// ids ARE capabilities: whoever knows one can POST to the automation role.
func NewEndpointID() string { return "ep_" + randomBase32(26) }

// NewChainID returns a fresh trace chain id.
func NewChainID() string { return "chn_" + randomBase32(20) }

// ValidEndpointID reports whether id has the shape NewEndpointID produces.
func ValidEndpointID(id string) bool {
	if !strings.HasPrefix(id, "ep_") || len(id) != 3+26 {
		return false
	}
	for _, c := range id[3:] {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

func nowString(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
