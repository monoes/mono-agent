package automation

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

// EngineVersion is the running engine's version, checked against a
// manifest's "engine" range. The CLI sets it from its build version at
// startup. A dev value ("", "dev", "0.0.0-dev") skips the check.
var EngineVersion = "0.0.0-dev"

// ManifestFile is the manifest's file name at the package root.
const ManifestFile = "automation.json"

// idPattern is the id slug. One character is allowed: the built-in "x".
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// ValidID reports whether id is a valid automation id slug.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// ParseManifest decodes automation.json.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	return m, nil
}

func readManifest(fsys fs.FS) (Manifest, error) {
	b, err := fs.ReadFile(fsys, ManifestFile)
	if err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", ManifestFile, err)
	}
	return ParseManifest(b)
}

// --- semver --------------------------------------------------------------

type semver struct {
	major, minor, patch int
	pre                 string
}

// parseSemver parses "1.2.3", "v1.2.3", "1.2.3-rc.1+build". Missing minor or
// patch parts are zero ("1.2" = "1.2.0") so engine ranges stay forgiving.
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v semver
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre, s = s[i+1:], s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 || parts[0] == "" {
		return v, false
	}
	nums := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	return v, true
}

// validSemver is strict: exactly three numeric parts.
func validSemver(s string) bool {
	core := strings.TrimSpace(s)
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	if strings.Count(core, ".") != 2 || strings.HasPrefix(core, "v") {
		return false
	}
	_, ok := parseSemver(s)
	return ok
}

func (a semver) cmp(b semver) int {
	for _, d := range [3]int{a.major - b.major, a.minor - b.minor, a.patch - b.patch} {
		if d != 0 {
			if d < 0 {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	case a.pre < b.pre:
		return -1
	default:
		return 1
	}
}

// CompareVersions compares two semver strings (-1, 0, 1). Unparseable
// versions sort before parseable ones and compare as strings among
// themselves.
func CompareVersions(a, b string) int {
	va, oka := parseSemver(a)
	vb, okb := parseSemver(b)
	switch {
	case oka && okb:
		return va.cmp(vb)
	case oka:
		return 1
	case okb:
		return -1
	default:
		return strings.Compare(a, b)
	}
}

// bumpPatch returns version with its patch number incremented and any
// prerelease dropped.
func bumpPatch(version string) string {
	v, ok := parseSemver(version)
	if !ok {
		return "1.0.0"
	}
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch+1)
}

// engineDev reports whether the running engine version skips range checks.
func engineDev(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" || strings.HasPrefix(v, "0.0.0") {
		return true
	}
	_, ok := parseSemver(v)
	return !ok
}

// EngineSatisfied reports whether engine version ver satisfies rng, a
// space-separated AND of comparators (">=0.68.0 <2.0.0", "^1.2.0",
// "~1.2.0", "=1.2.3", "1.2.3"). Only the numeric core of ver is compared,
// so a "v0.67.0-3-gabc" dev build counts as 0.67.0. An empty range or a dev
// engine always passes.
func EngineSatisfied(rng, ver string) (bool, error) {
	rng = strings.TrimSpace(rng)
	if rng == "" || engineDev(ver) {
		return true, nil
	}
	have, _ := parseSemver(ver)
	have.pre = ""
	for _, c := range strings.Fields(rng) {
		rest := strings.TrimLeft(c, "<>=^~")
		op := c[:len(c)-len(rest)]
		want, ok := parseSemver(rest)
		if !ok {
			return false, fmt.Errorf("bad engine range %q", rng)
		}
		var sat bool
		switch op {
		case ">=":
			sat = have.cmp(want) >= 0
		case ">":
			sat = have.cmp(want) > 0
		case "<=":
			sat = have.cmp(want) <= 0
		case "<":
			sat = have.cmp(want) < 0
		case "", "=", "==":
			sat = have.cmp(want) == 0
		case "^":
			upper := semver{major: want.major + 1}
			if want.major == 0 {
				upper = semver{minor: want.minor + 1}
			}
			sat = have.cmp(want) >= 0 && have.cmp(upper) < 0
		case "~":
			sat = have.cmp(want) >= 0 && have.cmp(semver{major: want.major, minor: want.minor + 1}) < 0
		default:
			return false, fmt.Errorf("bad engine range %q", rng)
		}
		if !sat {
			return false, nil
		}
	}
	return true, nil
}

// engineIssue returns an error issue when the manifest's engine range
// excludes the running engine (a malformed range is validateManifest's).
func engineIssue(m Manifest) *IssueJSON {
	ok, err := EngineSatisfied(m.Engine, EngineVersion)
	switch {
	case err != nil:
		return nil
	case !ok:
		return &IssueJSON{File: ManifestFile, Severity: "error", Code: "engine_mismatch",
			Message: fmt.Sprintf("requires engine %s, running %s", m.Engine, EngineVersion)}
	}
	return nil
}

// --- manifest checks -----------------------------------------------------

// validateManifest checks the manifest fields on their own (no files).
// source decides whether the imported-package rules apply.
func validateManifest(m Manifest, source string) []IssueJSON {
	var out []IssueJSON
	add := func(sev, code, format string, a ...any) {
		out = append(out, IssueJSON{File: ManifestFile, Severity: sev, Code: code, Message: fmt.Sprintf(format, a...)})
	}
	if m.Schema != SchemaV1 {
		add("error", "bad_schema", "schema must be %q, got %q", SchemaV1, m.Schema)
	}
	if !ValidID(m.ID) {
		add("error", "bad_id", "id %q must match %s", m.ID, idPattern.String())
	}
	if strings.TrimSpace(m.Name) == "" {
		add("error", "missing_name", "name is required")
	}
	if !validSemver(m.Version) {
		add("error", "bad_version", "version %q is not semver (x.y.z)", m.Version)
	}
	if m.Engine != "" {
		if _, err := EngineSatisfied(m.Engine, "999.0.0"); err != nil {
			add("error", "bad_engine", "%v", err)
		}
	}
	switch m.Policy.Tier {
	case "", "standard", "social":
	default:
		add("error", "bad_tier", "policy.tier must be standard or social, got %q", m.Policy.Tier)
	}
	for _, d := range m.Site.Domains {
		if !validDomainPattern(d) {
			add("error", "bad_domain", "site.domains entry %q is not a host or *.host glob", d)
		}
	}
	if len(m.Actions) == 0 {
		add("warning", "no_actions", "manifest lists no actions")
	}
	seen := map[string]bool{}
	for _, a := range m.Actions {
		if !safeName(a) {
			add("error", "bad_action_name", "action name %q is not a plain file name", a)
		}
		if seen[a] {
			add("error", "duplicate_action", "action %q listed twice", a)
		}
		seen[a] = true
	}
	for _, s := range m.Permissions.Scripts {
		if !safeName(s) || !strings.HasSuffix(s, ".js") {
			add("error", "bad_script_name", "permissions.scripts entry %q must be a .js file name", s)
		}
	}
	if source != SourceBuiltin {
		if len(m.Site.Domains) == 0 {
			add("error", "missing_domains", "non-built-in packages must declare site.domains")
		}
		if len(m.Permissions.Steps) == 0 {
			add("error", "missing_step_permissions", "non-built-in packages must declare permissions.steps")
		}
	}
	return out
}

var hostPattern = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:\d+)?$`)

func validDomainPattern(d string) bool { return hostPattern.MatchString(strings.ToLower(d)) }

// safeName reports whether s is a single path element safe to join under a
// package directory.
func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\:`) &&
		!strings.HasPrefix(s, ".") && fs.ValidPath(s)
}
