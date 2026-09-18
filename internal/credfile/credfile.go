// Package credfile checks that a credential file on disk can be read only by
// its owner before a secret is taken from it.
//
// On unix the check is the file mode: no group or other bits (0600 or
// stricter). Windows has no mode bits, so the check reads the file's owner and
// DACL instead: the owner must be the current user, SYSTEM or Administrators,
// and every ACE that grants access must name one of those. SYSTEM and
// Administrators can read any file anyway, much like root on unix.
package credfile

import (
	"errors"
	"os"
)

// Well-known SIDs trusted besides the current user.
const (
	sidLocalSystem    = "S-1-5-18"
	sidAdministrators = "S-1-5-32-544"
	sidOwnerRights    = "S-1-3-4"
)

// Windows ACE header values the DACL evaluation needs. They are copied here so
// the evaluation compiles, and is tested, on every platform.
const (
	aceInheritOnly = 0x08

	aceAccessDenied               = 0x01
	aceAccessDeniedObject         = 0x06
	aceAccessDeniedCallback       = 0x0A
	aceAccessDeniedCallbackObject = 0x0C
)

// ErrInsecure matches (errors.Is) every error CheckOwnerOnly returns for a
// credential file that other accounts could read or change.
var ErrInsecure = errors.New("credential file is not owner-only")

// insecureError carries a platform-specific requirement while matching
// ErrInsecure.
type insecureError struct{ requirement string }

func (e *insecureError) Error() string        { return e.requirement }
func (e *insecureError) Is(target error) bool { return target == ErrInsecure }

// CheckOwnerOnly returns nil when path, whose stat result is info, can be read
// only by its owner. It returns an error matching ErrInsecure when others can
// read it, and another error when its permissions cannot be read. The error
// text completes "credential file ..." and never contains the path or account
// names, so callers may return it to remote clients.
func CheckOwnerOnly(path string, info os.FileInfo) error {
	return checkOwnerOnly(path, info)
}

// aceEntry is one ACE of a DACL, reduced to what the evaluation needs.
type aceEntry struct {
	SID   string
	Type  uint8
	Flags uint8
	Mask  uint32
}

// windowsRequirement is the start of every ErrInsecure text on Windows.
const windowsRequirement = `must grant access only to its owner ` +
	`(fix with: icacls <file> /inheritance:r /grant:r "%USERNAME%:F")`

// evaluateDACL applies the Windows owner-only rule. trusted holds the SIDs
// that may own the file or be granted access: the current user, SYSTEM and
// Administrators. hasDACL false means a NULL DACL, which grants everyone full
// access. Deny ACEs, inherit-only ACEs (they apply to children, not to this
// file) and ACEs with an empty mask never make a file insecure.
func evaluateDACL(owner string, hasDACL bool, aces []aceEntry, trusted map[string]bool) error {
	if !trusted[owner] {
		return &insecureError{windowsRequirement + "; it is owned by another account"}
	}
	if !hasDACL {
		return &insecureError{windowsRequirement + "; it has no DACL, so everyone has access"}
	}
	for _, a := range aces {
		if a.Flags&aceInheritOnly != 0 || a.Mask == 0 || isDenyACE(a.Type) {
			continue
		}
		// OWNER RIGHTS names whoever owns the file, already checked above.
		if trusted[a.SID] || a.SID == sidOwnerRights {
			continue
		}
		return &insecureError{windowsRequirement + "; another account is granted access"}
	}
	return nil
}

func isDenyACE(t uint8) bool {
	switch t {
	case aceAccessDenied, aceAccessDeniedObject, aceAccessDeniedCallback, aceAccessDeniedCallbackObject:
		return true
	}
	return false
}

// trustedSIDs is the set evaluateDACL accepts for the given current user.
func trustedSIDs(currentUser string) map[string]bool {
	return map[string]bool{currentUser: true, sidLocalSystem: true, sidAdministrators: true}
}
