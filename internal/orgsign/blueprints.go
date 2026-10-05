package orgsign

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Blueprint digests (monomind#571/#575, catalog/blueprints.ts): a role that
// names a `blueprint` has its skills filled from the active, org-targeted
// catalog entry at start, so the signature covers that blueprint's bytes:
// "sha256:<hex of its blueprint.json>", keyed `blueprint:<name>` in
// monomind and projected as {"blueprints":{<name>: digest}} next to the
// definition and the instructions digests.
//
// monomind records a fixed "unavailable" string when it can't load the
// entry. This package never signs or verifies on that: a blueprint it
// can't load gives errUnknown (no Go hash), and monomind decides.

var (
	catalogNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	catalogShaRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// errBlueprintUnavailable: no active org blueprint of that name could be
// read and verified from the catalog (monomind's BLUEPRINT_UNAVAILABLE).
var errBlueprintUnavailable = fmt.Errorf("%w: a blueprint is not active for org on this machine", errUnknown)

// catalogEntry is the part of a catalog state entry the digest depends on.
type catalogEntry struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Status  string   `json:"status"`
	Sha256  string   `json:"sha256"`
	Targets []string `json:"targets"`
}

func catalogState(root string) string {
	return filepath.Join(root, ".monomind", "catalog", "state.json")
}

// activeBlueprint finds the active org-targeted blueprint entry `name` in
// the catalog state under root. Any unreadable or malformed state, or a
// repeated id, is "none" (monomind sees an empty catalog then).
func activeBlueprint(root, name string) (catalogEntry, bool) {
	var none catalogEntry
	b, err := os.ReadFile(catalogState(root))
	if err != nil || !catalogNameRe.MatchString(name) {
		return none, false
	}
	var st struct {
		SchemaVersion int            `json:"schemaVersion"`
		Entries       []catalogEntry `json:"entries"`
	}
	if json.Unmarshal(b, &st) != nil || st.SchemaVersion != 1 {
		return none, false
	}
	seen := map[string]bool{}
	var found *catalogEntry
	for i, e := range st.Entries {
		kind, n, ok := strings.Cut(e.ID, ":")
		if !ok || kind != e.Kind || !catalogNameRe.MatchString(n) || !catalogShaRe.MatchString(e.Sha256) || seen[e.ID] {
			return none, false
		}
		seen[e.ID] = true
		if e.ID == "blueprint:"+name {
			found = &st.Entries[i]
		}
	}
	if found == nil || found.Status != "active" {
		return none, false
	}
	for _, t := range found.Targets {
		if t == "org" {
			return *found, true
		}
	}
	return none, false
}

// packageFiles lists dir's files (relative, slash-separated, sorted by
// UTF-16 code unit as JavaScript's sort does); a symlink is an error.
func packageFiles(dir, prefix string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(prefix)))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		rel := e.Name()
		if prefix != "" {
			rel = prefix + "/" + e.Name()
		}
		switch {
		case e.Type()&os.ModeSymlink != 0:
			return nil, fmt.Errorf("symlink in package: %s", rel)
		case e.IsDir():
			sub, err := packageFiles(dir, rel)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		case e.Type().IsRegular():
			out = append(out, rel)
		}
	}
	sort.Slice(out, func(i, j int) bool { return utf16Less(out[i], out[j]) })
	return out, nil
}

// packageDigest is the catalog's content digest of a package dir: sha256
// over (u32 pathLen, path, u64 byteLen, bytes) for every file.
func packageDigest(dir string) (string, error) {
	files, err := packageFiles(dir, "")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		var lens [12]byte
		binary.BigEndian.PutUint32(lens[:4], uint32(len(rel)))
		binary.BigEndian.PutUint64(lens[4:], uint64(len(b)))
		h.Write(lens[:4])
		h.Write([]byte(rel))
		h.Write(lens[4:])
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// packageDir is `<packages>/<name>/<sha12>`, which must stay inside the
// store after resolving symlinks (verifyEntry's resolvePackageDir).
func packageDir(root, name string, e catalogEntry) (string, error) {
	store, err := filepath.EvalSymlinks(filepath.Join(root, ".monomind", "catalog", "packages"))
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(store, name, e.Sha256[:12]))
	if err != nil {
		return "", err
	}
	if !within(store, dir) || dir == store {
		return "", errors.New("package path escapes the store")
	}
	return dir, nil
}

// blueprintDigest is monomind's blueprintDigest(root, name): "sha256:<hex>"
// of the active org blueprint's blueprint.json, read from its package only
// after the package digest matches the catalog entry. Anything less is
// errBlueprintUnavailable.
func blueprintDigest(root, name string) (string, error) {
	root = realRoot(root)
	e, ok := activeBlueprint(root, name)
	if !ok {
		return "", errBlueprintUnavailable
	}
	dir, err := packageDir(root, name, e)
	if err != nil {
		return "", errBlueprintUnavailable
	}
	if got, err := packageDigest(dir); err != nil || got != e.Sha256 {
		return "", errBlueprintUnavailable
	}
	b, err := os.ReadFile(filepath.Join(dir, "blueprint.json"))
	if err != nil {
		return "", errBlueprintUnavailable
	}
	// The catalog snapshot parses the file as JSON (null fails there).
	var meta interface{}
	if json.Unmarshal(b, &meta) != nil || meta == nil {
		return "", errBlueprintUnavailable
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// blueprintNames are the distinct string `blueprint` values of v's roles,
// sorted: the names monomind digests.
func blueprintNames(v interface{}) []string {
	def, _ := v.(map[string]interface{})
	roles, _ := def["roles"].([]interface{})
	seen := map[string]bool{}
	var out []string
	for _, r := range roles {
		role, _ := r.(map[string]interface{})
		if n, ok := role["blueprint"].(string); ok && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// blueprintPaths are the files blueprint digests of v are read from under
// root: the catalog state and, for each blueprint whose package resolves,
// the package's files. Used to stamp them and to refuse symlinks on the way.
func blueprintPaths(root string, v interface{}) []string {
	names := blueprintNames(v)
	if len(names) == 0 {
		return nil
	}
	root = realRoot(root)
	out := []string{catalogState(root)}
	for _, n := range names {
		e, ok := activeBlueprint(root, n)
		if !ok {
			continue
		}
		dir := filepath.Join(root, ".monomind", "catalog", "packages", n, e.Sha256[:12])
		files, err := packageFiles(dir, "")
		if err != nil {
			continue
		}
		for _, f := range files {
			out = append(out, filepath.Join(dir, filepath.FromSlash(f)))
		}
	}
	return out
}

// blueprintDigests is the `blueprint:<name>` half of monomind's
// instructionsDigests: name to "sha256:<hex>" for each distinct blueprint a
// role names. One that can't be loaded fails the whole hash (errUnknown).
func blueprintDigests(v interface{}, dig digestFunc) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	for _, n := range blueprintNames(v) {
		d, err := dig(n)
		if err != nil {
			return nil, err
		}
		out[n] = d
	}
	return out, nil
}
