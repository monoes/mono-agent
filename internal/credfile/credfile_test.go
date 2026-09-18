package credfile

import (
	"errors"
	"strings"
	"testing"
)

const testUser = "S-1-5-21-1111111111-2222222222-3333333333-1001"

func TestEvaluateDACL(t *testing.T) {
	const (
		allow     = 0x00
		fullMask  = 0x1F01FF
		readMask  = 0x120089
		everyone  = "S-1-1-0"
		users     = "S-1-5-32-545"
		otherUser = "S-1-5-21-1111111111-2222222222-3333333333-1002"
	)
	cases := []struct {
		name    string
		owner   string
		hasDACL bool
		aces    []aceEntry
		want    string // "" = allowed; otherwise a substring of the error
	}{
		{"owner only", testUser, true, []aceEntry{{testUser, allow, 0, fullMask}}, ""},
		{"typical profile file", testUser, true, []aceEntry{
			{sidLocalSystem, allow, 0x10, fullMask},
			{sidAdministrators, allow, 0x10, fullMask},
			{testUser, allow, 0x10, fullMask},
		}, ""},
		{"empty DACL denies everyone", testUser, true, nil, ""},
		{"owned by administrators", sidAdministrators, true, []aceEntry{{testUser, allow, 0, fullMask}}, ""},
		{"owner rights", testUser, true, []aceEntry{{sidOwnerRights, allow, 0, fullMask}}, ""},
		{"everyone read", testUser, true, []aceEntry{
			{testUser, allow, 0, fullMask},
			{everyone, allow, 0, readMask},
		}, "another account is granted access"},
		{"users group inherited", testUser, true, []aceEntry{
			{testUser, allow, 0, fullMask},
			{users, allow, 0x10, readMask},
		}, "another account is granted access"},
		{"other user owns it", otherUser, true, []aceEntry{{testUser, allow, 0, fullMask}}, "owned by another account"},
		{"null DACL", testUser, false, nil, "no DACL"},
		{"deny ACE for everyone is fine", testUser, true, []aceEntry{
			{everyone, aceAccessDenied, 0, fullMask},
			{testUser, allow, 0, fullMask},
		}, ""},
		{"inherit-only ACE is ignored", testUser, true, []aceEntry{
			{testUser, allow, 0, fullMask},
			{"S-1-3-0", allow, aceInheritOnly | 0x03, fullMask},
		}, ""},
		{"empty mask is ignored", testUser, true, []aceEntry{
			{testUser, allow, 0, fullMask},
			{everyone, allow, 0, 0},
		}, ""},
		{"object allow ACE is checked", testUser, true, []aceEntry{
			{everyone, 0x05, 0, readMask},
		}, "another account is granted access"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := evaluateDACL(c.owner, c.hasDACL, c.aces, trustedSIDs(testUser))
			if c.want == "" {
				if err != nil {
					t.Fatalf("want allowed, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", c.want)
			}
			if !errors.Is(err, ErrInsecure) {
				t.Errorf("error %v does not match ErrInsecure", err)
			}
			if !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "icacls") {
				t.Errorf("error %q: want it to contain %q and the icacls fix", err, c.want)
			}
			if strings.Contains(err.Error(), "S-1-") {
				t.Errorf("error %q leaks a SID", err)
			}
		})
	}
}
