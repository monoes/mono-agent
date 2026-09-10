// cmd/monoagentcli/template_test.go
package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
)

// TestTemplateListTableRightAlignsNumericIDColumn guards against the same
// tablewriter v1 migration regression covered by login_test.go's
// TestLoginStatusTableRightAlignsNumericIDColumn: v0.0.5 auto-right-aligned
// any purely-numeric cell with no explicit SetColumnAlignment call; v1's
// shared newPlainTable helper has no such content-sniffing default, so
// `template list`'s numeric ID column silently went from right- to
// left-aligned. Seeds two templates with explicit, deliberately
// different-width IDs (1 vs 100, via direct SQL rather than the `create`
// command, which can't control the autoincrement value) and asserts the
// shorter ID is left-padded to align with the wider one.
func TestTemplateListTableRightAlignsNumericIDColumn(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	now := time.Now().UTC()
	insert := func(id int, name string) {
		t.Helper()
		if _, err := db.DB.Exec(
			`INSERT INTO templates (id, name, subject, body, created_at, updated_at) VALUES (?, ?, '', 'body', ?, ?)`,
			id, name, now, now,
		); err != nil {
			t.Fatalf("seeding template id=%d: %v", id, err)
		}
	}
	insert(1, "shortid")
	insert(100, "longid")
	if err := db.DB.Close(); err != nil {
		t.Fatalf("closing seed db: %v", err)
	}

	cfg := &globalConfig{DBPath: dbPath, JSONOutput: false}
	cmd := newTemplateCmd(cfg)
	cmd.SetArgs([]string{"list"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template list: %v", err)
	}

	lines := strings.Split(out.String(), "\n")
	var line1, line100 string
	for _, l := range lines {
		if strings.Contains(l, "shortid") {
			line1 = l
		}
		if strings.Contains(l, "longid") {
			line100 = l
		}
	}
	if line1 == "" || line100 == "" {
		t.Fatalf("expected both template rows in output, got:\n%s", out.String())
	}
	pos1 := strings.Index(line1, "1")
	pos100 := strings.Index(line100, "100")
	if pos1 <= pos100 {
		t.Fatalf("expected the single-digit ID to start further right than the 3-digit ID (right-alignment), got ID \"1\" at column %d and \"100\" at column %d\nrow1:   %q\nrow100: %q", pos1, pos100, line1, line100)
	}
}
