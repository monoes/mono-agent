package recordanalyze

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

var conflictSelectorRe = regexp.MustCompile(`selectors\.json#([^,\s]+)`)

// keepPackageSelectors makes the staged draft use the target package's
// current entry for every selector key both define differently (e.g. after
// `automation rerecord` updated the package), so the merge keeps the
// package's newer selector. Only the staged copy changes; the draft on disk
// is left alone. Returns the keys it kept.
func keepPackageSelectors(reg Installer, stage, id string) ([]string, error) {
	p, err := reg.Get(id)
	if err != nil || p == nil {
		return nil, nil // new automation: nothing to keep
	}
	pkgSels, err := p.Selectors()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(stage, "selectors.json")
	draft := map[string]action.SelectorEntry{}
	if err := readJSON(path, &draft); err != nil {
		return nil, nil
	}
	var kept []string
	for k, mine := range draft {
		theirs, ok := pkgSels[k]
		if !ok || sameSelectorContent(mine, theirs) {
			continue
		}
		draft[k] = theirs
		kept = append(kept, k)
	}
	if len(kept) == 0 {
		return nil, nil
	}
	sort.Strings(kept)
	return kept, writeJSON(path, draft)
}

// sameSelectorContent compares entries ignoring verifiedAt.
func sameSelectorContent(a, b action.SelectorEntry) bool {
	a.VerifiedAt, b.VerifiedAt = "", ""
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// SelectorConflictError is a save refused because the draft's selectors
// differ from the target package's current ones. It keeps the message the
// CLI always printed and carries the keys, so callers (the side panel via
// `record save --json`) branch on code/keys instead of matching text.
type SelectorConflictError struct {
	Keys []string
	Err  error
}

// SelectorConflictCode is the JSON error code of a SelectorConflictError.
const SelectorConflictCode = "selector_conflict"

func (e *SelectorConflictError) Error() string {
	return fmt.Sprintf("%v\nselector(s) %s differ from the package's current ones (changed since this draft was recorded, e.g. by `automation rerecord`); save with --keep-package-selectors to keep the package's",
		e.Err, strings.Join(e.Keys, ", "))
}

func (e *SelectorConflictError) Unwrap() error { return e.Err }

// JSONErrorFields is what `--json` adds next to "error".
func (e *SelectorConflictError) JSONErrorFields() map[string]any {
	return map[string]any{"code": SelectorConflictCode, "keys": e.Keys}
}

// withSelectorConflictHint names the selector keys of a merge conflict and
// points at --keep-package-selectors, as a *SelectorConflictError.
func withSelectorConflictHint(err error) error {
	if err == nil {
		return nil
	}
	var keys []string
	seen := map[string]bool{}
	for _, m := range conflictSelectorRe.FindAllStringSubmatch(err.Error(), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			keys = append(keys, m[1])
		}
	}
	if len(keys) == 0 {
		return err
	}
	return &SelectorConflictError{Keys: keys, Err: err}
}
