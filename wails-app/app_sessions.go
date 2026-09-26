package main

import (
	"fmt"
	"strconv"
)

// ─────────────────────────────────────────────────────────────────────────────
// Sessions — browser logins, through `monoagentcli login …`
// ─────────────────────────────────────────────────────────────────────────────

type SessionInfo struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Platform string `json:"platform"`
	Expiry   string `json:"expiry"`
	AddedAt  string `json:"added_at"`
	Active   bool   `json:"active"`
}

// cliLoginStatusRow is one `login status --json` row.
type cliLoginStatusRow struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Platform  string `json:"platform"`
	Expiry    string `json:"expiry"`
	WhenAdded string `json:"when_added"`
	Status    string `json:"status"` // active | expired | logged_out
}

// GetSessions lists the active profile's saved browser sessions. An
// installed automation without a session (a logged_out row, which has no
// id) is not a session and is left out.
func (a *App) GetSessions() []SessionInfo {
	var rows []cliLoginStatusRow
	if err := a.runMonoCLI("", &rows, "login", "status"); err != nil {
		return nil
	}
	sessions := make([]SessionInfo, 0, len(rows))
	for _, r := range rows {
		if r.Status == "logged_out" || r.ID == 0 {
			continue
		}
		sessions = append(sessions, SessionInfo{
			ID:       r.ID,
			Username: r.Username,
			Platform: r.Platform,
			Expiry:   r.Expiry,
			AddedAt:  r.WhenAdded,
			Active:   r.Status == "active",
		})
	}
	return sessions
}

// TestSession checks that a browser session exists, hasn't expired and has
// its cookies stored: "ok", or "error: <reason>".
func (a *App) TestSession(id int) string {
	if err := a.runConnCLI("", nil, "login", "test", strconv.Itoa(id)); err != nil {
		return "error: " + err.Error()
	}
	return "ok"
}

// DeleteSession deletes a browser session and the vault entry holding its
// cookies.
func (a *App) DeleteSession(id int) error {
	if err := a.deleteSession(id); err != nil {
		return err
	}
	a.emitLog("SESSIONS", "WARN", fmt.Sprintf("Deleted session ID %d", id))
	return nil
}

func (a *App) deleteSession(id int) error {
	return a.runConnCLI("", nil, "login", "delete", strconv.Itoa(id))
}
