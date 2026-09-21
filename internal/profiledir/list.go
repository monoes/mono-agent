package profiledir

import (
	"context"
	"database/sql"
	"fmt"
)

// Listing the profiles a user actually has.
//
// The filesystem half of this package answers "where does profile X live";
// this answers "which profiles are there", for the surfaces that have to
// offer a choice — the extension's profile picker (profile.list on the
// request channel) and `capture list --profile`. Both need the same three
// things: the id, the name a person recognises, and which one is current.

// ActiveProfileSetting is the settings row naming the current profile. The
// CLI's `profile switch` writes it and `profile list` marks it with a star.
const ActiveProfileSetting = "active_profile_id"

// Profile is one profile as a chooser shows it. ID is the value that must
// travel on a capture envelope; Name is the label.
type Profile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

// List returns every profile, oldest first, with exactly one marked
// Default: the active profile if there is one, otherwise the oldest, so a
// caller always has something to fall back to. A database with no profiles
// yields an empty list and no error — that is a legitimate state (a fresh
// install), not a failure.
//
// Ids that could not be used as a path component (see ValidProfileID) are
// skipped: nothing should be able to offer one as a capture destination.
// The CLI mints UUIDs, so this only ever fires on a hand-edited database.
func List(ctx context.Context, db *sql.DB) ([]Profile, error) {
	if db == nil {
		return nil, fmt.Errorf("profiledir: no database")
	}

	// Read the active id first and finish with it: running a second query
	// while the profiles cursor is open can deadlock a pool capped to one
	// connection (see cmd/monoagentcli/profile.go, which learned this).
	var activeID string
	_ = db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, ActiveProfileSetting).Scan(&activeID)

	rows, err := db.QueryContext(ctx, `SELECT id, name FROM profiles ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("profiledir: list profiles: %w", err)
	}
	defer rows.Close()

	out := []Profile{}
	for rows.Next() {
		var p Profile
		var name sql.NullString
		if err := rows.Scan(&p.ID, &name); err != nil {
			continue
		}
		if !ValidProfileID(p.ID) {
			continue
		}
		p.Name = name.String
		if p.Name == "" {
			p.Name = p.ID
		}
		p.Default = p.ID == activeID
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("profiledir: list profiles: %w", err)
	}

	marked := false
	for _, p := range out {
		if p.Default {
			marked = true
			break
		}
	}
	if !marked && len(out) > 0 {
		out[0].Default = true
	}
	return out, nil
}

// Default returns the id of the profile List marks as default, or "" when
// there are none.
func Default(ctx context.Context, db *sql.DB) string {
	profiles, err := List(ctx, db)
	if err != nil {
		return ""
	}
	for _, p := range profiles {
		if p.Default {
			return p.ID
		}
	}
	return ""
}
