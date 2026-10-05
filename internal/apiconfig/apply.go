package apiconfig

import (
	"context"
	"database/sql"
	"slices"
	"sort"
	"strings"
)

// Change is what one `api config set|unset`, one MCP call or one save of the app asks for.
type Change struct {
	// Set maps a setting (its key, or the dashed spelling of its flag) to the text of its new
	// value, in the syntax of its environment variable. An empty or blank value is refused: a
	// saved value is removed with Unset.
	Set map[string]string
	// Unset lists settings whose saved value is removed. One that is not saved is not an error.
	Unset []string
	// All removes every saved setting. It cannot be combined with Set or Unset. It is also the
	// repair: a saved row that cannot be decoded (ErrDamaged) is removed, and the result says so
	// (RemovedUnreadableRow), but only with Confirm, since what the row limited cannot be told
	// (the Widening with the key WideningKeySavedSettings). A row in a newer format (ErrTooNew)
	// is never removed: that is an error, confirmed or not.
	All bool
	// Confirm opens the widening gate: the CLI's --yes, the MCP server's --allow-api-exposure. A
	// change that makes the server reach further is refused without it, and so is the removal of
	// a row that cannot be read.
	Confirm bool
	// DryRun computes the document the change would give, writes nothing, and never refuses a
	// widening: its Widening says what the change would do.
	DryRun bool
}

// ChangeResult is the document of `api config set|unset --json` and of the MCP tool
// api_config_set: the config document of the state after the change, and three fields.
type ChangeResult struct {
	ConfigReport
	// Applied: the saved settings are now as asked (also when nothing needed changing); false for
	// a dry run.
	Applied bool `json:"applied"`
	// Changed are the settings whose saved value is different after the change (newly saved,
	// changed or removed), in the order of the settings.
	Changed []string `json:"changed"`
	// Widening says how the change makes the server reach further; [] when it does not.
	Widening []Widening `json:"widening"`
	// RemovedUnreadableRow: the saved row could not be read (not a JSON object, a version that
	// is not a whole number from 1, a known field of the wrong type) and unset all, confirmed,
	// removed it; for a dry run, would remove it. Widening then holds the one for a row that
	// cannot be read. Omitted when it did not. Only unset all does this, and never to a row in
	// a newer format, which is an ErrTooNew.
	RemovedUnreadableRow bool `json:"removed_unreadable_row,omitempty"`
}

// WideningError is the error of a change that makes the server reach further when it was not
// confirmed. Nothing was written.
type WideningError struct{ Widening []Widening }

func (e *WideningError) Error() string {
	reasons := make([]string, len(e.Widening))
	for i, w := range e.Widening {
		reasons[i] = w.Reason
	}
	return "this change makes the server reach further: " + strings.Join(reasons, " ")
}

// Apply reads the saved document, applies the change, checks it, runs Widens and writes the
// result, all inside one BEGIN IMMEDIATE transaction (see Update), so the gate judges the row
// that is really replaced and two callers never lose each other's change. The document it
// returns is built after the commit.
//
// A change that fails its checks (an unknown key, a value that fails its rule, nothing to
// change, a TLS file given without the other, a key both set and unset) is a *ValidationError
// that names the settings and the rules; a change that makes the server reach further, without
// Confirm, a *WideningError. Neither writes anything or asks the service manager. Only what the
// change touches is checked: an invalid value somebody left in the document does not stop a
// change that does not touch it, and Unset removes it.
//
// A row that cannot be decoded is an ErrDamaged for every change but one: All removes it (and
// says so in RemovedUnreadableRow), since nothing else could, but what the row limited cannot be
// told, so that is a widening of unknown size: without Confirm and without DryRun it is a
// *WideningError with the one Widening of the key WideningKeySavedSettings, a dry run reports
// it, and nothing is written. A row in a newer format is an ErrTooNew for every change, All
// and Confirm included: it holds what a newer version saved.
func Apply(ctx context.Context, db *sql.DB, env Env, ch Change) (ChangeResult, error) {
	req, err := parseChange(ch)
	if err != nil {
		return ChangeResult{}, err
	}
	var after Settings
	var changed []string
	var widening []Widening
	repaired, err := update(ctx, db, updateOpts{dry: ch.DryRun, repair: ch.All}, func(s *Settings, repairing bool) error {
		before := *s
		next := before
		for _, key := range req.unset {
			next.Unset(key)
		}
		for key, text := range req.set {
			_ = next.Set(key, text)
		}
		if req.touchesTLS {
			if hasCert, hasKey := next.TLSCertFile != "", next.TLSKeyFile != ""; hasCert != hasKey {
				key := KeyTLSKeyFile
				if hasCert {
					key = KeyTLSCertFile
				}
				return &ValidationError{Problems: []Problem{{Key: key, Message: "tls_cert_file and tls_key_file must be set together"}}}
			}
		}
		for _, key := range Keys() {
			if savedText(before, key) != savedText(next, key) {
				changed = append(changed, key)
			}
		}
		widening = Widens(before, next)
		if repairing {
			// What the row limited cannot be told: Widens judged two empty documents, and removing
			// a row that cannot be read may reach further than anything, so it needs confirmation.
			widening = append([]Widening{unreadableRowWidening()}, widening...)
		}
		if len(widening) > 0 && !ch.Confirm && !ch.DryRun {
			return &WideningError{Widening: widening}
		}
		*s, after = next, next
		return nil
	})
	if err != nil {
		return ChangeResult{}, err
	}
	if changed == nil {
		changed = []string{}
	}
	if widening == nil {
		widening = []Widening{}
	}
	return ChangeResult{ConfigReport: report(ctx, after, env), Applied: !ch.DryRun, Changed: changed, Widening: widening, RemovedUnreadableRow: repaired}, nil
}

// request is a Change that passed its checks: keys in their canonical spelling, values in the
// spelling they are stored in.
type request struct {
	set        map[string]string
	unset      []string
	touchesTLS bool
}

// parseChange checks a change without the database: the keys, the values and the combinations.
func parseChange(ch Change) (request, error) {
	var problems []Problem
	add := func(key, msg string) {
		p := Problem{Key: key, Message: msg}
		if !slices.Contains(problems, p) {
			problems = append(problems, p)
		}
	}
	req := request{set: map[string]string{}}
	switch {
	case ch.All && len(ch.Unset) > 0:
		add("", "unset all cannot be combined with keys to unset: give the keys, or all")
	case ch.All && len(ch.Set) > 0:
		add("", "unset all cannot be combined with a setting to set")
	case len(ch.Set) == 0 && len(ch.Unset) == 0 && !ch.All:
		add("", "nothing to change: give a setting to set or to unset, or unset all")
	}

	named := map[string]bool{} // the keys given to Set, whether or not their values were good
	names := make([]string, 0, len(ch.Set))
	for name := range ch.Set {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		key, ok := LookupKey(name)
		if !ok {
			add("", errUnknownKey().Error())
			continue
		}
		if named[key] {
			add(key, key+" is given twice")
			continue
		}
		named[key] = true
		text := ch.Set[name]
		if strings.TrimSpace(text) == "" {
			add(key, key+" must not be empty: remove a saved value with unset")
			continue
		}
		canon, err := Canonical(key, text)
		if err != nil {
			add(key, err.Error())
			continue
		}
		req.set[key] = canon
	}
	for _, name := range ch.Unset {
		key, ok := LookupKey(name)
		if !ok {
			add("", errUnknownKey().Error())
			continue
		}
		if named[key] {
			add(key, key+" is both set and unset")
			continue
		}
		if !slices.Contains(req.unset, key) {
			req.unset = append(req.unset, key)
		}
	}
	if len(problems) > 0 {
		sort.SliceStable(problems, func(i, j int) bool { return keyIndex(problems[i].Key) < keyIndex(problems[j].Key) })
		return request{}, &ValidationError{Problems: problems}
	}
	if ch.All {
		req.unset = Keys()
	}
	for key := range req.set {
		req.touchesTLS = req.touchesTLS || isTLS(key)
	}
	for _, key := range req.unset {
		req.touchesTLS = req.touchesTLS || isTLS(key)
	}
	return req, nil
}

// keyIndex orders problems by the order of the settings, those of no setting first.
func keyIndex(key string) int {
	if i := slices.Index(Keys(), key); i >= 0 {
		return i + 1
	}
	return 0
}
