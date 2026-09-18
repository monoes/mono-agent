//go:build windows

package credfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// setDACL replaces path's DACL with one granting each SID full access, with
// inheritance from the parent directory removed.
func setDACL(t *testing.T, path string, sids ...*windows.SID) {
	t.Helper()
	var entries []windows.EXPLICIT_ACCESS
	for _, sid := range sids {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCheckOwnerOnlyWindowsACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cred")
	if err := os.WriteFile(path, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	user := tu.User.Sid
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	check := func() error {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return CheckOwnerOnly(path, info)
	}

	setDACL(t, path, user)
	if err := check(); err != nil {
		t.Fatalf("owner-only DACL: want allowed, got %v", err)
	}
	setDACL(t, path, user, everyone)
	if err := check(); !errors.Is(err, ErrInsecure) {
		t.Fatalf("Everyone granted: want ErrInsecure, got %v", err)
	}
	setDACL(t, path, user, users)
	if err := check(); !errors.Is(err, ErrInsecure) {
		t.Fatalf("Users granted: want ErrInsecure, got %v", err)
	}
	// Restore access so t.TempDir cleanup can remove the file.
	setDACL(t, path, user)
}
