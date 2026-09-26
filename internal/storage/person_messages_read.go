package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Read state of inbound person messages (migration 050): read_at NULL means
// unread. Outbound messages are never unread.

func nullTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// MarkPersonMessagesRead marks inbound messages of the profile read: the
// given ids, or every message of personID when ids is empty. It returns how
// many were unread before.
func (d *Database) MarkPersonMessagesRead(profileID, personID string, ids []string) (int, error) {
	if profileID == "" {
		profileID = "default"
	}
	if personID == "" && len(ids) == 0 {
		return 0, fmt.Errorf("name messages or a person to mark read")
	}
	q := `UPDATE person_messages SET read_at = ?
		WHERE profile_id = ? AND direction = 'inbound' AND read_at IS NULL`
	args := []interface{}{time.Now().UTC(), profileID}
	if len(ids) > 0 {
		q += " AND id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if personID != "" {
		q += " AND person_id = ?"
		args = append(args, personID)
	}
	res, err := d.DB.Exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("marking messages read: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// MarkPersonMessageUnread marks one inbound message of the profile unread.
func (d *Database) MarkPersonMessageUnread(profileID, id string) error {
	if profileID == "" {
		profileID = "default"
	}
	res, err := d.DB.Exec(`UPDATE person_messages SET read_at = NULL
		WHERE id = ? AND profile_id = ? AND direction = 'inbound'`, id, profileID)
	if err != nil {
		return fmt.Errorf("marking message %s unread: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("inbound message %s not found", id)
	}
	return nil
}

// CountUnreadPersonMessages counts the profile's unread inbound messages.
func (d *Database) CountUnreadPersonMessages(profileID string) (int, error) {
	if profileID == "" {
		profileID = "default"
	}
	var n int
	err := d.DB.QueryRow(`SELECT COUNT(*) FROM person_messages
		WHERE profile_id = ? AND direction = 'inbound' AND read_at IS NULL`, profileID).Scan(&n)
	return n, err
}
