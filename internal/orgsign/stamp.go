package orgsign

import (
	"os"
	"path/filepath"
	"sort"
)

// Stamp identifies the exact files a definition's hash is computed from:
// the org JSON and every instructions file it names, each by its identity
// and times (on POSIX: device, inode, size, mtime and ctime; ctime can't be
// set back from user space). Two equal stamps around monomind's review
// mean nobody swapped a file in for that read and restored it after (#295
// review): the review then showed the content the hash is taken from.
//
// Every directory between the project root (exclusive) and each stamped
// file is stamped too (device, inode, ctime): renaming `.monomind/orgs`,
// `.monomind` or an instructions file's folder away, putting a fresh one
// with benign files in its place for monomind's read, and renaming the
// real one back leaves the files' own stamps as they were. The root itself
// is stamped by identity only (device, inode), since unrelated work in the
// project changes its times.
// Swapping the project root itself is out of this model (trust.go).
type Stamp map[string]fileStamp

// fileStamp is one path's lstat and stat identity; exists is false for a
// path that isn't there.
type fileStamp struct {
	exists bool
	// symlink marks a path component that is a symlink: its target can be
	// swapped without the stamp moving, so a stamp holding one is never
	// Same as anything (Untrusted explains why).
	symlink    bool
	link, file fileID
}

func stampPath(path string) fileStamp {
	lst, err := os.Lstat(path)
	if err != nil {
		return fileStamp{}
	}
	fs := fileStamp{exists: true, link: idOf(lst), symlink: lst.Mode()&os.ModeSymlink != 0}
	if st, err := os.Stat(path); err == nil {
		fs.file = idOf(st)
	}
	return fs
}

// stampDir stamps a directory by what a swap of it changes.
func stampDir(path string) fileStamp {
	st, err := os.Lstat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, link: dirIDOf(st), symlink: st.Mode()&os.ModeSymlink != 0}
}

// stampRoot stamps the project root by identity alone.
func stampRoot(path string) fileStamp {
	st, err := os.Lstat(path)
	if err != nil {
		return fileStamp{}
	}
	id := dirIDOf(st)
	id.ctimeNs = 0
	return fileStamp{exists: true, link: id}
}

// addParents stamps each directory from file's parent up to root
// (exclusive). A file outside root gets no parent stamps: such a file
// can't be hashed here, so nothing is signed from it anyway.
func (s Stamp) addParents(root, file string) {
	for dir := filepath.Dir(file); dir != root && within(root, dir); dir = filepath.Dir(dir) {
		s["dir:"+dir] = stampDir(dir)
	}
}

// StampDefinition stamps org's JSON under root and each instructions file
// the JSON names now (resolved as instructionsDigest resolves them), with
// the directories above them and the root's identity.
func StampDefinition(root, org string) (Stamp, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(absRoot, ".monomind", "orgs", org+".json")
	s := Stamp{path: stampPath(path), "root:" + absRoot: stampRoot(absRoot)}
	s.addParents(absRoot, path)
	raw, _, err := ReadFile(root, org)
	if err != nil {
		return nil, err
	}
	var files []string
	if v, err := parseOrgJSON(raw); err == nil {
		_, _ = instructionsDigests(v, func(file string) (string, error) {
			files = append(files, file)
			return "", nil
		})
	}
	sort.Strings(files)
	rroot := realRoot(root)
	s["root:"+rroot] = stampRoot(rroot)
	for _, f := range files {
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(rroot, p)
		}
		s[p] = stampPath(p)
		s.addParents(rroot, p)
		if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
			s[real] = stampPath(real)
			s.addParents(rroot, real)
		}
	}
	return s, nil
}

// Same reports whether s and o stamp the same files, unchanged, with no
// symlink among them (a symlink's target can change under an equal stamp).
func (s Stamp) Same(o Stamp) bool {
	if s == nil || o == nil || len(s) != len(o) || s.hasSymlink() || o.hasSymlink() {
		return false
	}
	for k, v := range s {
		if w, ok := o[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func (s Stamp) hasSymlink() bool {
	for _, v := range s {
		if v.symlink {
			return true
		}
	}
	return false
}
