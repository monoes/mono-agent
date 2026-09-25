package automation

import (
	"database/sql"
	"log"
	"time"
)

// Stale selector health: rows for keys the installed package version no
// longer declares (a new version renamed or dropped the selector). Doctor
// flags them as "stale"; the recorder prunes the ones untouched for
// staleRowTTL, at most once per pruneInterval, after a flush.

const (
	staleRowTTL   = 30 * 24 * time.Hour
	pruneInterval = 24 * time.Hour
)

// DeclaredSelectorKeys returns the selector keys of the package's
// selectors.json — the keys an effective selector can exist for (the
// overlay only reorders or replaces declared entries).
func DeclaredSelectorKeys(p *Package) (map[string]bool, error) {
	sels, err := p.Selectors()
	if err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(sels))
	for k := range sels {
		keys[k] = true
	}
	return keys, nil
}

// SelectorKeysFunc reports the declared selector keys of an installed
// automation. ok is false when they are unknown (not installed,
// unreadable); nothing of that automation is pruned then.
type SelectorKeysFunc func(automationID string) (keys map[string]bool, ok bool)

// RegistrySelectorKeys resolves declared keys through reg.
func RegistrySelectorKeys(reg *Registry) SelectorKeysFunc {
	return func(id string) (map[string]bool, bool) {
		if reg == nil {
			return nil, false
		}
		p, err := reg.Get(id)
		if err != nil {
			return nil, false
		}
		keys, err := DeclaredSelectorKeys(p)
		return keys, err == nil
	}
}

// bootedKeys resolves keys through the booted registry at call time.
func bootedKeys(id string) (map[string]bool, bool) {
	return RegistrySelectorKeys(bootedRegistry())(id)
}

// PruneStaleSelectorHealth removes rows whose key the installed package no
// longer declares and that were last updated before cutoff. It returns the
// number of rows removed.
func PruneStaleSelectorHealth(db *sql.DB, keysOf SelectorKeysFunc, cutoff time.Time) (int, error) {
	at := formatHealthTime(cutoff)
	rows, err := db.Query(`SELECT automation_id, selector_key FROM automation_selector_health WHERE updated_at < ?`, at)
	if err != nil {
		return 0, err
	}
	var victims []healthKey
	known := map[string]map[string]bool{}
	unknown := map[string]bool{}
	for rows.Next() {
		var k healthKey
		if err := rows.Scan(&k.id, &k.key); err != nil {
			rows.Close()
			return 0, err
		}
		if unknown[k.id] {
			continue
		}
		keys, seen := known[k.id]
		if !seen {
			var ok bool
			if keys, ok = keysOf(k.id); !ok {
				unknown[k.id] = true
				continue
			}
			known[k.id] = keys
		}
		if !keys[k.key] {
			victims = append(victims, k)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, k := range victims {
		res, err := db.Exec(`DELETE FROM automation_selector_health
			WHERE automation_id = ? AND selector_key = ? AND updated_at < ?`, k.id, k.key, at)
		if err != nil {
			return n, err
		}
		c, _ := res.RowsAffected()
		n += int(c)
	}
	return n, nil
}

// maybePrune prunes stale rows when a key source is set and pruneInterval
// has passed since the last prune. Loop goroutine only.
func (r *HealthRecorder) maybePrune() {
	if r.opts.SelectorKeys == nil {
		return
	}
	now := r.now()
	if !r.lastPrune.IsZero() && now.Sub(r.lastPrune) < pruneInterval {
		return
	}
	r.lastPrune = now
	if _, err := PruneStaleSelectorHealth(r.db, r.opts.SelectorKeys, now.Add(-staleRowTTL)); err != nil {
		log.Printf("automation: selector health: prune stale rows: %v", err)
	}
}
