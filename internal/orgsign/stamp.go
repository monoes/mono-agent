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
type Stamp map[string]fileStamp

// fileStamp is one path's lstat and stat identity; exists is false for a
// path that isn't there.
type fileStamp struct {
	exists     bool
	link, file fileID
}

func stampPath(path string) fileStamp {
	lst, err := os.Lstat(path)
	if err != nil {
		return fileStamp{}
	}
	fs := fileStamp{exists: true, link: idOf(lst)}
	if st, err := os.Stat(path); err == nil {
		fs.file = idOf(st)
	}
	return fs
}

// StampDefinition stamps org's JSON under root and each instructions file
// the JSON names now (resolved as instructionsDigest resolves them).
func StampDefinition(root, org string) (Stamp, error) {
	path := filepath.Join(root, ".monomind", "orgs", org+".json")
	s := Stamp{path: stampPath(path)}
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
	for _, f := range files {
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(rroot, p)
		}
		s[p] = stampPath(p)
		if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
			s[real] = stampPath(real)
		}
	}
	return s, nil
}

// Same reports whether s and o stamp the same files, unchanged.
func (s Stamp) Same(o Stamp) bool {
	if s == nil || o == nil || len(s) != len(o) {
		return false
	}
	for k, v := range s {
		if w, ok := o[k]; !ok || w != v {
			return false
		}
	}
	return true
}
