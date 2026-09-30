// Package orgsign checks monomind 2.21's operator signatures on org
// definitions (monomind#502, orgrt/org-signature.ts) so mono-agent can keep
// them intact without defeating them: a write mono-agent makes to an org
// file is re-signed only when the file verified before the write and the
// document came from exactly those bytes (guard.go); anything else is left
// for the operator to review and sign.
//
// Verification is done here, in Go, because monomind has no
// machine-readable verify command: `org status --format json` carries no
// signature state and `org sign` without --yes only prints a human review.
// This package reimplements the signed projection, its hash and the HMAC
// check against the sidecar with the operator key. It only ever reads the
// operator dir; signing is always monomind's (`monomind org sign --yes`).
// Where this code cannot reproduce monomind's hash with certainty (an
// instructions_file it would refuse to read), the answer is StateUnknown,
// which never leads to a signature.
package orgsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Signature states: monomind's OrgSignatureReason, plus "signed" and
// "unknown" (this package could not decide; treated as not verified).
const (
	StateSigned       = "signed"
	StateUnsigned     = "unsigned"
	StateChanged      = "changed"
	StateInvalid      = "invalid-signature"
	StateForbiddenKey = "forbidden-key"
	StateUnknown      = "unknown"
)

// Status is one org definition's signature state.
type Status struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// OK reports whether the definition verified.
func (s Status) OK() bool { return s.State == StateSigned }

// Refused reports whether monomind 2.21 would refuse to start or reload
// the org for this state. StateUnknown is not a refusal: monomind decides.
func (s Status) Refused() bool {
	switch s.State {
	case StateUnsigned, StateChanged, StateInvalid, StateForbiddenKey:
		return true
	}
	return false
}

const signatureVersion = 1

var (
	unsignedOrgFields  = map[string]bool{"goal": true, "status": true}
	unsignedRoleFields = map[string]bool{"title": true, "responsibilities": true, "ui": true}
	forbiddenKeys      = map[string]bool{"__proto__": true, "constructor": true, "prototype": true}
	// monomind's assertSafeOrgName.
	safeOrgName = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9_-]*$`)
	// file-roots.ts DASHBOARD_CREDENTIAL.
	dashboardCredential = regexp.MustCompile(`^dashboard-token(-\d+)?$`)
)

// errUnknown marks a hash this package can't reproduce with certainty.
var errUnknown = errors.New("cannot reproduce monomind's hash for this definition")

// OperatorDir is monomind's defaultOperatorDir(): where the operator key
// and the signature sidecars live.
func OperatorDir() string {
	if d := os.Getenv("MONOMIND_ORGRT_OPERATOR_DIR"); d != "" {
		if abs, err := filepath.Abs(d); err == nil {
			return abs
		}
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".monomind", "orgrt-operator")
}

// realRoot is monomind's projectRoot(): the real path, else the absolute one.
func realRoot(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// SignaturePath is where monomind keeps org's signature for project root.
func SignaturePath(root, org string) (string, error) {
	if !safeOrgName.MatchString(org) {
		return "", fmt.Errorf("invalid org name: %s", org)
	}
	sum := sha256.Sum256([]byte(realRoot(root)))
	return filepath.Join(OperatorDir(), "org-signatures", hex.EncodeToString(sum[:])[:24], org+".json"), nil
}

// readRecord reads a sidecar: (nil, "") when there is none, or a problem
// when it can't be trusted (untrustedFileReason).
func readRecord(path string) (map[string]interface{}, string) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, ""
	}
	if bad := untrusted(st, "signature "+path, false); bad != "" {
		return nil, bad
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Sprintf("signature %s is unreadable (%v)", path, err)
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, fmt.Sprintf("signature %s is unreadable (%v)", path, err)
	}
	rec, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Sprintf("signature %s is not an object", path)
	}
	return rec, ""
}

// untrusted is access-grant-key.ts's untrustedFileReason: a symlink, the
// wrong kind of file, another user's, or open to group/other.
func untrusted(st fs.FileInfo, what string, dir bool) string {
	if st.Mode()&fs.ModeSymlink != 0 {
		return what + " is a symlink"
	}
	if dir && !st.IsDir() {
		return what + " is not a directory"
	}
	if !dir && !st.Mode().IsRegular() {
		return what + " is not a regular file"
	}
	if msg := foreignOwner(st, what); msg != "" {
		return msg
	}
	if !dir && looseMode(st) {
		return fmt.Sprintf("%s has mode %o (must be 600)", what, st.Mode().Perm())
	}
	return ""
}

// keyUnreadable prefixes loadKey's problem when the key exists and is
// trusted but this process can't read it.
const keyUnreadable = "\x00unreadable:"

// loadKey reads the operator key the way loadOperatorKey does (never
// creating it). The operator dir's own mode is not checked: monomind
// tightens a dir of ours to 0700 before trusting it, and the key and every
// sidecar must be ours and owner-only regardless.
func loadKey(dir string) ([]byte, string) {
	path := filepath.Join(dir, "full-access-grant.key")
	st, err := os.Lstat(path)
	if err != nil {
		return nil, "no operator key at " + path
	}
	if dst, err := os.Lstat(dir); err == nil {
		if bad := untrusted(dst, "operator dir "+dir, true); bad != "" {
			return nil, bad
		}
	}
	if bad := untrusted(st, "operator key "+path, false); bad != "" {
		return nil, bad
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Sprintf("%soperator key %s is unreadable (%v)", keyUnreadable, path, err)
	}
	if len(key) == 0 {
		return nil, fmt.Sprintf("operator key %s is empty", path)
	}
	return key, ""
}

// Describe says what the state means for the definition, as monomind's
// orgSignatureMessage does ("the definition has no operator signature").
func (s Status) Describe() string {
	switch s.State {
	case StateSigned:
		return "the definition is signed"
	case StateUnsigned:
		return "the definition has no operator signature"
	case StateChanged:
		return "the definition changed since the operator signed it"
	case StateForbiddenKey:
		return fmt.Sprintf("the definition holds a forbidden key (%s) — remove it", s.Detail)
	case StateInvalid:
		return fmt.Sprintf("the definition has an operator signature that does not verify (%s)", s.Detail)
	}
	return "the definition's signature could not be checked here (" + s.Detail + ")"
}

// Message is monomind's orgSignatureMessage, with mono-agent's sign command.
func Message(org string, st Status) string {
	if st.OK() {
		return fmt.Sprintf("org %s: %s", org, st.Describe())
	}
	return fmt.Sprintf("org %s: %s — review it, then sign it: monoagentcli org sign %s", org, st.Describe(), org)
}

// Verify checks raw (the bytes of <root>/.monomind/orgs/<org>.json)
// against the operator's signature, as monomind's verifyOrgDef does.
func Verify(root, org string, raw []byte) Status {
	v, err := parseJSON(raw)
	if err != nil {
		return Status{State: StateUnknown, Detail: "unreadable JSON: " + err.Error()}
	}
	if p := forbiddenKeyPath(v, ""); p != "" {
		return Status{State: StateForbiddenKey, Detail: p}
	}
	path, err := SignaturePath(root, org)
	if err != nil {
		return Status{State: StateUnknown, Detail: err.Error()}
	}
	rec, problem := readRecord(path)
	if problem != "" {
		return Status{State: StateInvalid, Detail: problem}
	}
	if rec == nil {
		return Status{State: StateUnsigned}
	}
	dir := OperatorDir()
	key, problem := loadKey(dir)
	if detail, ok := strings.CutPrefix(problem, keyUnreadable); ok {
		// This process can't read the key (monomind can): no verdict here.
		return Status{State: StateUnknown, Detail: detail}
	}
	if problem != "" {
		return Status{State: StateInvalid, Detail: problem}
	}
	sig, sigOK := rec["sig"].(string)
	hash, hashOK := rec["hash"].(string)
	if !sigOK || !hashOK {
		return Status{State: StateInvalid, Detail: "the signature file is malformed"}
	}
	at, atOK := jsString(rec["at"])
	if !atOK {
		return Status{State: StateUnknown, Detail: "the signature's timestamp is not a string"}
	}
	v0, hasV := rec["v"]
	input := hmacInput(v0, hasV, org, realRoot(root), hash, at)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(input))
	want := mac.Sum(nil)
	got, err := hex.DecodeString(evenHexPrefix(sig))
	if err != nil || !hmac.Equal(want, got) {
		return Status{State: StateInvalid, Detail: "the HMAC does not match the operator key"}
	}
	h, err := hashValue(v, root)
	if err != nil {
		return Status{State: StateUnknown, Detail: err.Error()}
	}
	if h != hash {
		return Status{State: StateChanged}
	}
	return Status{State: StateSigned}
}

// hmacInput is the string monomind's hmac() signs: JSON.stringify of an
// object literal, so its keys keep the literal's order (kind, v, org, root,
// hash, at), and a missing `v` (undefined) is left out.
func hmacInput(v interface{}, hasV bool, org, root, hash, at string) string {
	var sb strings.Builder
	sb.WriteString(`{"kind":"org-def"`)
	if hasV {
		sb.WriteString(`,"v":`)
		writeValue(&sb, v)
	}
	for _, kv := range [][2]string{{"org", org}, {"root", root}, {"hash", hash}, {"at", at}} {
		sb.WriteString(`,"` + kv[0] + `":`)
		writeString(&sb, kv[1])
	}
	sb.WriteByte('}')
	return sb.String()
}

// evenHexPrefix is what Buffer.from(s, 'hex') decodes: the longest prefix
// of hex digit pairs.
func evenHexPrefix(s string) string {
	n := 0
	for n < len(s) && strings.ContainsRune("0123456789abcdefABCDEF", rune(s[n])) {
		n++
	}
	return s[:n-n%2]
}

// jsString is String(x) for the values a sidecar's `at` can hold.
func jsString(x interface{}) (string, bool) {
	switch t := x.(type) {
	case string:
		return t, true
	case json.Number:
		return stringify(t), true
	case bool:
		return stringify(t), true
	case nil:
		return "", false
	}
	return "", false
}

// Hash is monomind's computeOrgDefHash(raw, root): the sha256 of the
// signed projection, with the digests of any instructions files.
func Hash(root string, raw []byte) (string, error) {
	v, err := parseJSON(raw)
	if err != nil {
		return "", err
	}
	if p := forbiddenKeyPath(v, ""); p != "" {
		return "", fmt.Errorf("holds a forbidden key (%s)", p)
	}
	return hashValue(v, root)
}

func hashValue(v interface{}, root string) (string, error) {
	proj := projection(v)
	digests, err := instructionsDigests(v, root)
	if err != nil {
		return "", err
	}
	if len(digests) > 0 {
		proj = map[string]interface{}{"definition": proj, "instructions": digests}
	}
	sum := sha256.Sum256([]byte(stringify(proj)))
	return hex.EncodeToString(sum[:]), nil
}

// projection is orgSignatureInput without the canonical sort (stringify
// orders keys itself).
func projection(v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	out := map[string]interface{}{}
	for k, val := range m {
		if unsignedOrgFields[k] {
			continue
		}
		if roles, isArr := val.([]interface{}); k == "roles" && isArr {
			rs := make([]interface{}, len(roles))
			for i, r := range roles {
				rs[i] = signedRole(r)
			}
			val = rs
		}
		out[k] = val
	}
	return out
}

func signedRole(r interface{}) interface{} {
	m, ok := r.(map[string]interface{})
	if !ok {
		return r
	}
	out := map[string]interface{}{}
	for k, val := range m {
		if !unsignedRoleFields[k] {
			out[k] = val
		}
	}
	return out
}

// forbiddenKeyPath is monomind's forbiddenKeyPath.
func forbiddenKeyPath(v interface{}, path string) string {
	switch t := v.(type) {
	case []interface{}:
		for i, e := range t {
			if hit := forbiddenKeyPath(e, fmt.Sprintf("%s[%d]", path, i)); hit != "" {
				return hit
			}
		}
	case map[string]interface{}:
		// Go maps lose the source order, so which forbidden key is reported
		// first can differ from monomind — never whether one is.
		for _, k := range propertyOrder(t) {
			here := k
			if path != "" {
				here = path + "." + k
			}
			if forbiddenKeys[k] {
				return here
			}
			if hit := forbiddenKeyPath(t[k], here); hit != "" {
				return hit
			}
		}
	}
	return ""
}

// SignedHash is the hash recorded in org's signature sidecar, without
// checking the HMAC — used right after monomind signs, to confirm what it
// signed is what mono-agent wrote.
func SignedHash(root, org string) (string, bool) {
	path, err := SignaturePath(root, org)
	if err != nil {
		return "", false
	}
	rec, problem := readRecord(path)
	if rec == nil || problem != "" {
		return "", false
	}
	h, ok := rec["hash"].(string)
	return h, ok
}

// Withdraw removes org's signature (and the projection kept beside it), so
// the org is unsigned: used only when monomind signed bytes other than the
// ones mono-agent wrote.
func Withdraw(root, org string) error {
	path, err := SignaturePath(root, org)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(strings.TrimSuffix(path, ".json") + ".projection.json"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ReadFile reads org's definition under root: its bytes and sha256.
func ReadFile(root, org string) ([]byte, string, error) {
	if !safeOrgName.MatchString(org) {
		return nil, "", fmt.Errorf("invalid org name: %s", org)
	}
	b, err := os.ReadFile(filepath.Join(root, ".monomind", "orgs", org+".json"))
	if err != nil {
		return nil, "", err
	}
	return b, SHA256(b), nil
}

// SHA256 is the hex sha256 of b (orgdesign.Save's return value).
func SHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// VerifyFile verifies org's definition as it is on disk now.
func VerifyFile(root, org string) (Status, string, error) {
	b, sha, err := ReadFile(root, org)
	if err != nil {
		return Status{}, "", err
	}
	return Verify(root, org, b), sha, nil
}
