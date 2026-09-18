//go:build windows

package credfile

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// checkOwnerOnly reads the file's owner and DACL and applies evaluateDACL.
// The mode Go reports on Windows only reflects the read-only attribute, so
// info is not used.
func checkOwnerOnly(path string, _ os.FileInfo) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("permissions cannot be read: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("owner cannot be read: %v", err)
	}
	user, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("current user cannot be identified: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil && !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		return fmt.Errorf("permissions cannot be read: %w", err)
	}
	hasDACL := err == nil && dacl != nil
	var aces []aceEntry
	if hasDACL {
		if aces, err = daclEntries(dacl); err != nil {
			return fmt.Errorf("permissions cannot be read: %w", err)
		}
	}
	return evaluateDACL(owner.String(), hasDACL, aces, trustedSIDs(user))
}

// daclEntries lists the ACEs of dacl.
func daclEntries(dacl *windows.ACL) ([]aceEntry, error) {
	out := make([]aceEntry, 0, dacl.AceCount)
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return nil, err
		}
		sid := aceSID(ace)
		if sid == nil || !sid.IsValid() {
			return nil, fmt.Errorf("ACE %d has no valid SID", i)
		}
		out = append(out, aceEntry{
			SID:   sid.String(),
			Type:  ace.Header.AceType,
			Flags: ace.Header.AceFlags,
			Mask:  uint32(ace.Mask),
		})
	}
	return out, nil
}

// isObjectACE reports the ACCESS_{ALLOWED,DENIED}_OBJECT_ACE types and their
// callback variants. After Mask they carry a flags word and up to two GUIDs
// before the SID; every other DACL ACE type has the SID right after Mask.
func isObjectACE(t uint8) bool { return t == 0x05 || t == 0x06 || t == 0x0B || t == 0x0C }

const (
	aceObjectTypePresent          = 0x1
	aceInheritedObjectTypePresent = 0x2
	guidSize                      = 16
)

func aceSID(ace *windows.ACCESS_ALLOWED_ACE) *windows.SID {
	start := unsafe.Pointer(&ace.SidStart)
	if isObjectACE(ace.Header.AceType) {
		flags := *(*uint32)(start)
		off := uintptr(4)
		if flags&aceObjectTypePresent != 0 {
			off += guidSize
		}
		if flags&aceInheritedObjectTypePresent != 0 {
			off += guidSize
		}
		start = unsafe.Add(start, off)
	}
	return (*windows.SID)(start)
}

func currentUserSID() (string, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return tu.User.Sid.String(), nil
}
