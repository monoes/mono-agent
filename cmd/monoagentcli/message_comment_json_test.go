package main

import (
	"encoding/json"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

// fetchLatestTargetMetadata returns the metadata blob of the most recently
// inserted action_targets row.
func fetchLatestTargetMetadata(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.Close()
	var meta string
	if err := db.DB.QueryRow(`SELECT metadata FROM action_targets ORDER BY rowid DESC LIMIT 1`).Scan(&meta); err != nil {
		t.Fatalf("reading latest action_targets row: %v", err)
	}
	return meta
}

// TestMessageCmdEncodesQuoteBearingUsername guards the json.Marshal fix in
// `message`: a username containing a double quote used to be Sprintf-ed into
// the action_targets metadata blob, producing corrupt JSON that no consumer
// could parse. Execution is expected to fail fast afterward (social platform
// not compiled in); the target row is inserted before that point.
func TestMessageCmdEncodesQuoteBearingUsername(t *testing.T) {
	dbPath := newMessagesCLITestDB(t)
	username := `weird"name`

	cmd := newMessageCmd(&globalConfig{DBPath: dbPath, ProfileID: "default"})
	cmd.SetArgs([]string{"instagram", username, "--text", "hello"})
	// Error is fine (platform not compiled in / no browser); the insert has
	// already happened by then.
	_ = cmd.Execute()

	meta := fetchLatestTargetMetadata(t, dbPath)
	var decoded map[string]string
	if err := json.Unmarshal([]byte(meta), &decoded); err != nil {
		t.Fatalf("target metadata is not valid JSON: %q (%v)", meta, err)
	}
	if decoded["username"] != username {
		t.Errorf("metadata username = %q, want %q", decoded["username"], username)
	}
}

// TestCommentCmdEncodesQuoteBearingURL is the comment.go counterpart: a post
// URL containing a double quote must round-trip through the metadata blob.
func TestCommentCmdEncodesQuoteBearingURL(t *testing.T) {
	dbPath := newMessagesCLITestDB(t)
	postURL := `https://x.com/p/abc?u="q"`

	cmd := newCommentCmd(&globalConfig{DBPath: dbPath, ProfileID: "default"})
	cmd.SetArgs([]string{"instagram", postURL, "--text", "nice"})
	_ = cmd.Execute()

	meta := fetchLatestTargetMetadata(t, dbPath)
	var decoded map[string]string
	if err := json.Unmarshal([]byte(meta), &decoded); err != nil {
		t.Fatalf("target metadata is not valid JSON: %q (%v)", meta, err)
	}
	if decoded["url"] != postURL {
		t.Errorf("metadata url = %q, want %q", decoded["url"], postURL)
	}
}
