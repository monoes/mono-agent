package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

func TestTemplateListJSONIsAnArray(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	run := func() string {
		var runErr error
		out := captureStdout(t, func() {
			cmd := newTemplateCmd(&globalConfig{DBPath: dbPath, JSONOutput: true})
			cmd.SetArgs([]string{"list"})
			runErr = cmd.Execute()
		})
		if runErr != nil {
			t.Fatal(runErr)
		}
		return out
	}
	if out := strings.TrimSpace(run()); out != "[]" {
		t.Fatalf("empty template list = %q, want []", out)
	}
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO templates (name, subject, body) VALUES ('Hello', 'Hi', 'Body text')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var got []map[string]interface{}
	if err := json.Unmarshal([]byte(run()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["name"] != "Hello" || got[0]["subject"] != "Hi" || got[0]["body"] != "Body text" || got[0]["id"] == nil {
		t.Fatalf("template list = %+v", got)
	}
}
