package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/spf13/cobra"
)

// profileOrDefault is the profile a command acts on: --profile (or the
// active one initDB resolved), "default" when neither is set.
func profileOrDefault(cfg *globalConfig) string {
	if cfg.ProfileID == "" {
		return "default"
	}
	return cfg.ProfileID
}

// sessionByID loads one of the active profile's browser sessions. The
// cookies themselves stay in the vault: only whether a vault entry is
// linked is reported.
func sessionByID(ctx context.Context, db *sql.DB, profileID string, id int) (sessionRow, bool, error) {
	var s sessionRow
	var vaultRef string
	err := db.QueryRowContext(ctx,
		`SELECT id, username, platform, expiry, when_added, COALESCE(vault_ref,'')
		 FROM crawler_sessions WHERE id = ? AND profile_id = ?`, id, profileID,
	).Scan(&s.ID, &s.Username, &s.Platform, &s.Expiry, &s.WhenAdded, &vaultRef)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, errNotFound("session not found")
	}
	if err != nil {
		return s, false, fmt.Errorf("reading session: %w", err)
	}
	return s, vaultRef != "", nil
}

// parseSessionID reads a `login test|delete` id argument.
func parseSessionID(arg string) (int, error) {
	id, err := strconv.Atoi(arg)
	if err != nil || id <= 0 {
		return 0, errInvalidInput("session id must be a positive integer, got %q", arg)
	}
	return id, nil
}

// newLoginTestCmd checks one saved browser session: it must exist in the
// active profile, not be expired, and have its cookies in the vault. It
// does not open a browser or contact the platform.
func newLoginTestCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "test <session-id>",
		Short: "Check a saved browser session (exists, not expired, cookies stored)",
		Long: "Checks a session from `login status` without opening a browser: exit code 2 when the id " +
			"is unknown in the active profile, 4 when it is expired or has no cookies stored.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseSessionID(args[0])
			if err != nil {
				return err
			}
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			s, hasCookies, err := sessionByID(cmd.Context(), db.DB, profileOrDefault(cfg), id)
			if err != nil {
				return err
			}
			if !s.Expiry.After(time.Now()) {
				return errAuthConnection("session expired")
			}
			if !hasCookies {
				return errAuthConnection("no cookies stored")
			}
			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), map[string]any{
					"id": s.ID, "platform": s.Platform, "username": s.Username,
					"expiry": s.Expiry.UTC().Format(time.RFC3339), "status": "ok",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %s session for %s is valid until %s\n", s.Platform, s.Username, s.Expiry.Format("2006-01-02 15:04"))
			return nil
		},
	}
}

// newLoginDeleteCmd deletes one saved browser session by id, with the vault
// entry holding its cookies.
func newLoginDeleteCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <session-id>",
		Short: "Delete one saved browser session and its stored cookies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseSessionID(args[0])
			if err != nil {
				return err
			}
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			n, err := deleteSessions(cmd.Context(), db.DB, profileOrDefault(cfg), "id = ?", id)
			if err != nil {
				return err
			}
			if n == 0 {
				return errNotFound("session %d not found", id)
			}
			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), map[string]any{"id": id, "deleted": true})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted session %d.\n", id)
			return nil
		},
	}
}

// deleteSessions deletes the active profile's crawler_sessions rows matching
// where (a trusted SQL fragment; values go in args) together with the vault
// entries holding their cookies, and returns how many rows went. A vault
// entry that cannot be removed is reported on stderr, not fatal: the
// session itself is gone either way.
func deleteSessions(ctx context.Context, db *sql.DB, profileID, where string, args ...any) (int64, error) {
	qargs := append(append([]any{}, args...), profileID)
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(vault_ref,'') FROM crawler_sessions WHERE `+where+` AND profile_id = ?`, qargs...)
	if err != nil {
		return 0, fmt.Errorf("reading sessions: %w", err)
	}
	var refs []string
	for rows.Next() {
		var ref string
		if rows.Scan(&ref) == nil && ref != "" {
			refs = append(refs, ref)
		}
	}
	rows.Close()

	res, err := db.ExecContext(ctx, `DELETE FROM crawler_sessions WHERE `+where+` AND profile_id = ?`, qargs...)
	if err != nil {
		return 0, fmt.Errorf("deleting sessions: %w", err)
	}
	for _, ref := range refs {
		if err := secrets.Delete(ctx, db, profileID, ref); err != nil {
			fmt.Fprintf(os.Stderr, "warning: session deleted but its vault entry %s could not be removed: %v\n", ref, err)
		}
	}
	return res.RowsAffected()
}
