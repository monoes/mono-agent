package ai

import "fmt"

// CoderWorkspace is one folder coder conversations have run in.
type CoderWorkspace struct {
	Path          string `json:"path"`
	LastUsed      string `json:"lastUsed"`
	Conversations int    `json:"conversations"`
}

// ListCoderWorkspaces returns profileID's coder folders, most recently used
// first.
func (s *AIStore) ListCoderWorkspaces(profileID string, limit int) ([]CoderWorkspace, error) {
	if profileID == "" {
		profileID = "default"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT cwd, MAX(updated_at), COUNT(*) FROM ai_chat_conversations
		WHERE profile_id = ? AND mode = ? AND cwd != ''
		GROUP BY cwd ORDER BY MAX(updated_at) DESC LIMIT ?`, profileID, ModeCoder, limit)
	if err != nil {
		return nil, fmt.Errorf("list coder workspaces: %w", err)
	}
	defer rows.Close()
	out := []CoderWorkspace{}
	for rows.Next() {
		var w CoderWorkspace
		if err := rows.Scan(&w.Path, &w.LastUsed, &w.Conversations); err != nil {
			return nil, fmt.Errorf("scan coder workspace: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
