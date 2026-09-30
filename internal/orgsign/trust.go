package orgsign

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// When the stamps can't vouch for a definition's files, nothing signs on
// their word: no review hash (Review & sign needs monomind's own reviewed
// hash then) and no automatic re-sign.
//
//   - A symlink anywhere between the project root and the org JSON or an
//     instructions file: its target can be swapped without touching
//     anything on the stamped (lexical) path. Orgs under a symlinked
//     .monomind are signed with `monomind org sign` in a terminal.
//   - A filesystem whose file identities or times can't be trusted (NFS,
//     SMB/CIFS, FUSE, vfat, exFAT).
//   - Windows, where os.FileInfo gives no file index or change time
//     (platformUntrusted).
//
// Swapping the project root itself is out of this model: it needs write
// access to the root's parent, and a role with that much access can
// already read the operator key.

// untrustedFS are filesystem types whose identities and times the stamps
// can't rely on (a remote server, or a FUSE daemon, controls them; FAT
// and exFAT have no inodes).
var untrustedFS = []string{"nfs", "smb", "cifs", "fuse", "vfat", "msdos", "exfat"}

// fsTypeOf names the filesystem path is on ("" when unknown); a var so
// tests can stand in for statfs.
var fsTypeOf = statfsType

// Untrusted says why the stamps can't vouch for org's files under root,
// or "" when they can.
func Untrusted(root, org string) string {
	if reason := platformUntrusted(); reason != "" {
		return reason
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err.Error()
	}
	paths := [][2]string{{absRoot, filepath.Join(absRoot, ".monomind", "orgs", org+".json")}}
	if raw, _, err := ReadFile(root, org); err == nil {
		if v, err := parseOrgJSON(raw); err == nil {
			rroot := realRoot(root)
			_, _ = instructionsDigests(v, func(file string) (string, error) {
				p := file
				if !filepath.IsAbs(p) {
					p = filepath.Join(rroot, p)
				}
				paths = append(paths, [2]string{rroot, p})
				return "", nil
			})
		}
	}
	for _, rp := range paths {
		base, path := rp[0], rp[1]
		for p := path; p != base && within(base, p); p = filepath.Dir(p) {
			if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
				return fmt.Sprintf("%s is a symlink, so a swap of its target can't be seen here — sign this org with `monomind org sign %s` in a terminal", p, org)
			}
		}
		for _, p := range []string{base, filepath.Dir(path)} {
			t := strings.ToLower(fsTypeOf(p))
			for _, bad := range untrustedFS {
				if t != "" && strings.Contains(t, bad) {
					return fmt.Sprintf("%s is on a %s filesystem, whose file identities can't be trusted here — sign this org with `monomind org sign %s` in a terminal", p, t, org)
				}
			}
		}
	}
	return ""
}

// OnlyDirTimesMoved reports that s and o differ only in the times of
// directories (same files, same directory identities): something wrote
// into .monomind while monomind reviewed, which a review may retry. It
// never makes the review count: a retry must come back unchanged.
func (s Stamp) OnlyDirTimesMoved(o Stamp) bool {
	if s == nil || o == nil || len(s) != len(o) || s.Same(o) || s.hasSymlink() || o.hasSymlink() {
		return false
	}
	for k, v := range s {
		w, ok := o[k]
		if !ok {
			return false
		}
		if v == w {
			continue
		}
		if !strings.HasPrefix(k, "dir:") || v.exists != w.exists {
			return false
		}
		a, b := v.link, w.link
		a.ctimeNs, b.ctimeNs = 0, 0
		if a != b {
			return false
		}
	}
	return true
}
