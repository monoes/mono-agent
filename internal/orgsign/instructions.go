package orgsign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Instructions files (monomind's instructions-file.ts): a role's or
// loadout's instructions_file is signed by the digest of its content.

// digestFunc gives an instructions_file's digest ("sha256:<hex>").
type digestFunc func(file string) (string, error)

// readDigests digests each file as it is on disk now.
func readDigests(root string) digestFunc {
	return func(file string) (string, error) { return instructionsDigest(file, root) }
}

// Pins are the instructions-file digests a verified definition was
// checked with, keyed by the instructions_file value. Signing with them,
// rather than with the files as they are when monomind signs, is what
// keeps a file edited in between from being signed (monomind hashes the
// file itself, so the result then differs and the signature is withdrawn).
type Pins map[string]string

// pinned digests only files in p; any other is a reference this package
// never verified.
func (p Pins) pinned(file string) (string, error) {
	if d, ok := p[file]; ok {
		return d, nil
	}
	return "", fmt.Errorf("instructions_file %s was not part of the verified definition", file)
}

// recordingDigests is readDigests that also records each digest in pins.
func recordingDigests(root string, pins Pins) digestFunc {
	return func(file string) (string, error) {
		d, err := instructionsDigest(file, root)
		if err == nil {
			pins[file] = d
		}
		return d, err
	}
}

// instructionsDigests is monomind's instructionsDigests: "role:<id>" and
// "loadout:<name>" to "sha256:<hex>" of each instructions_file.
func instructionsDigests(v interface{}, dig digestFunc) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	def, _ := v.(map[string]interface{})
	if roles, ok := def["roles"].([]interface{}); ok {
		for _, r := range roles {
			role, _ := r.(map[string]interface{})
			file, ok := role["instructions_file"].(string)
			if !ok {
				continue
			}
			id, ok := jsIDString(role["id"], role != nil && hasKey(role, "id"))
			if !ok {
				return nil, errUnknown
			}
			d, err := dig(file)
			if err != nil {
				return nil, err
			}
			out["role:"+id] = d
		}
	}
	var loadouts map[string]interface{}
	switch t := def["loadouts"].(type) {
	case map[string]interface{}:
		loadouts = t
	case []interface{}:
		loadouts = map[string]interface{}{}
		for i, e := range t {
			loadouts[fmt.Sprint(i)] = e
		}
	}
	for name, l := range loadouts {
		lm, _ := l.(map[string]interface{})
		file, ok := lm["instructions_file"].(string)
		if !ok {
			continue
		}
		d, err := dig(file)
		if err != nil {
			return nil, err
		}
		out["loadout:"+name] = d
	}
	return out, nil
}

func hasKey(m map[string]interface{}, k string) bool { _, ok := m[k]; return ok }

// jsIDString is String(role.id) for the id types it can reasonably be.
func jsIDString(x interface{}, present bool) (string, bool) {
	if !present {
		return "undefined", true
	}
	switch t := x.(type) {
	case string:
		return t, true
	case json.Number, bool:
		return stringify(t), true
	case nil:
		return "null", true
	}
	return "", false
}

// instructionsDigest is "sha256:<hex>" of an instructions file monomind
// would read. Every case where monomind would refuse the file (and sign
// "unreadable: <its error text>", which can't be reproduced here) is
// errUnknown, as is any doubt about which case applies.
func instructionsDigest(file, root string) (string, error) {
	rroot := realRoot(root)
	lexical := file
	if !filepath.IsAbs(file) {
		lexical = filepath.Join(rroot, file)
	}
	real, err := filepath.EvalSymlinks(lexical)
	if err != nil || !within(rroot, real) {
		return "", errUnknown
	}
	home, _ := os.UserHomeDir()
	if home != "" && within(rroot, home) {
		// The project holds the home dir, so a denied dir may be inside it.
		return "", errUnknown
	}
	denied := []string{OperatorDir(root)}
	for _, p := range []string{".ssh", ".git-credentials", ".config/git/credentials", ".config/gh", ".netrc",
		".monomind/orgrt-operator", ".monomind/dashboard-auth"} {
		denied = append(denied, filepath.Join(home, p))
	}
	for _, d := range denied {
		if rd, err := filepath.EvalSymlinks(d); err == nil {
			d = rd
		}
		if within(d, real) {
			return "", errUnknown
		}
	}
	if dashboardCredential.MatchString(filepath.Base(real)) {
		return "", errUnknown
	}
	st, err := os.Lstat(real)
	if err != nil || !st.Mode().IsRegular() || multiplyLinked(st) {
		return "", errUnknown
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return "", errUnknown
	}
	// readFileSync(fd, 'utf-8') then hash the string: invalid UTF-8 would be
	// replaced first, so only hash text that is valid as is.
	if !utf8.Valid(b) {
		return "", errUnknown
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func within(container, target string) bool {
	if container == target {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(container, sep) {
		container += sep
	}
	return strings.HasPrefix(target, container)
}
