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

// withSelectorConflictHint names the selector keys of a merge conflict and
// points at --keep-package-selectors.
func withSelectorConflictHint(err error) error {
	if err == nil {
		return nil
	}
	var keys []string
	for _, m := range conflictSelectorRe.FindAllStringSubmatch(err.Error(), -1) {
		keys = append(keys, m[1])
	}
	if len(keys) == 0 {
		return err
	}
	return fmt.Errorf("%w\nselector(s) %s differ from the package's current ones (changed since this draft was recorded, e.g. by `automation rerecord`); save with --keep-package-selectors to keep the package's",
		err, strings.Join(keys, ", "))
}
