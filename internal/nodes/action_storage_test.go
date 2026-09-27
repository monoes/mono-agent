package nodes

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

func newTargetsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "targets.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	return db.DB
}

// `node run` executes a browser node standalone under ExecutionID "cli",
// which has no workflow_executions row: saving extracted items must keep
// the targets unattached instead of failing the action on the FK.
func TestSaveExtractedDataWithoutWorkflowExecution(t *testing.T) {
	db := newTargetsDB(t)

	s := &workflowActionStorage{db: db, executionID: "cli", nodeID: "cli-node", platform: "hackernews"}
	items := []map[string]interface{}{{"url": "https://news.ycombinator.com/item?id=1"}}
	if err := s.SaveExtractedData("a", items); err != nil {
		t.Fatalf("SaveExtractedData: %v", err)
	}

	var execID sql.NullString
	if err := db.QueryRow(`SELECT execution_id FROM workflow_node_targets WHERE node_id = 'cli-node'`).Scan(&execID); err != nil {
		t.Fatalf("reading target: %v", err)
	}
	if execID.Valid {
		t.Errorf("execution_id = %q, want NULL for a standalone run", execID.String)
	}
}

func TestSaveExtractedDataAttachesToWorkflowExecution(t *testing.T) {
	db := newTargetsDB(t)

	if _, err := db.Exec(`INSERT INTO workflows (id, name) VALUES ('w1', 'w')`); err != nil {
		t.Fatalf("seeding workflow: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workflow_executions (id, workflow_id) VALUES ('e1', 'w1')`); err != nil {
		t.Fatalf("seeding execution: %v", err)
	}

	s := &workflowActionStorage{db: db, executionID: "e1", nodeID: "n1", platform: "hackernews"}
	if err := s.SaveExtractedData("a", []map[string]interface{}{{"url": "https://news.ycombinator.com/item?id=1"}}); err != nil {
		t.Fatalf("SaveExtractedData: %v", err)
	}

	var execID sql.NullString
	if err := db.QueryRow(`SELECT execution_id FROM workflow_node_targets WHERE node_id = 'n1'`).Scan(&execID); err != nil {
		t.Fatalf("reading target: %v", err)
	}
	if execID.String != "e1" {
		t.Errorf("execution_id = %v, want e1", execID)
	}
}

// Saving extracted LinkedIn posts made each post a "person" named after
// its permalink's last segment (urn:li:activity:…). A post is not a person:
// its author is, and a post without one links to nobody.
func TestSaveExtractedDataLinksPostAuthorsNotPosts(t *testing.T) {
	db := newTargetsDB(t)
	s := &workflowActionStorage{db: db, profileID: "p1", executionID: "cli", nodeID: "cli-node", platform: "linkedin"}
	items := []map[string]interface{}{
		{"url": "https://www.linkedin.com/feed/update/urn:li:activity:7509522219027554305/", "urn": "urn:li:activity:7509522219027554305",
			"author": "Ada First", "author_url": "https://www.linkedin.com/in/ada-first-test/", "text_preview": "Hello"},
		{"url": "https://www.linkedin.com/feed/update/urn:li:activity:7508827577256460288/",
			"author": "Example Works", "author_url": "https://www.linkedin.com/company/example-works/"},
		{"url": "https://www.linkedin.com/feed/update/urn:li:activity:7509235263131398145/"},
	}
	if err := s.SaveExtractedData("a", items); err != nil {
		t.Fatalf("SaveExtractedData: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM people WHERE platform_username LIKE 'urn:%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("urn people = %d (%v), want none", n, err)
	}
	var username, fullName, profileURL string
	if err := db.QueryRow(`SELECT platform_username, full_name, profile_url FROM people WHERE profile_id = 'p1'`).Scan(&username, &fullName, &profileURL); err != nil {
		t.Fatalf("author row: %v", err)
	}
	if username != "ada-first-test" || fullName != "Ada First" || profileURL != "https://www.linkedin.com/in/ada-first-test/" {
		t.Fatalf("author = %s / %s / %s", username, fullName, profileURL)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM people`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("people = %d (%v), want only the member author", n, err)
	}
	// Every post is still a target; only the first is linked (to its author).
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_node_targets WHERE node_id = 'cli-node'`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("targets = %d (%v), want 3", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_node_targets WHERE person_id IS NOT NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("linked targets = %d (%v), want 1", n, err)
	}
}

// A profile read fills the person's profile columns, experience and
// education included.
func TestSaveExtractedDataStoresProfileDetails(t *testing.T) {
	db := newTargetsDB(t)
	s := &workflowActionStorage{db: db, profileID: "p1", executionID: "cli", nodeID: "cli-node", platform: "linkedin"}
	items := []map[string]interface{}{{
		"username": "ada-first-test", "profile_url": "https://www.linkedin.com/in/ada-first-test/",
		"full_name": "Ada First", "headline": "Robotics engineer", "job_title": "Engineer",
		"location": "Lisbon, Portugal", "about": "Builds things.", "profile_picture_url": "https://media.example/ada.jpg",
		"experience": []interface{}{map[string]interface{}{"title": "Engineer", "company": "Example Works"}},
		"education":  []interface{}{map[string]interface{}{"school": "Example University"}},
	}}
	if err := s.SaveExtractedData("a", items); err != nil {
		t.Fatalf("SaveExtractedData: %v", err)
	}
	var fullName, headline, jobTitle, location, about, imageURL, experience, education string
	if err := db.QueryRow(`SELECT full_name, headline, job_title, location, about, image_url, experience, education FROM people WHERE platform_username = 'ada-first-test'`).
		Scan(&fullName, &headline, &jobTitle, &location, &about, &imageURL, &experience, &education); err != nil {
		t.Fatalf("person: %v", err)
	}
	if fullName != "Ada First" || headline != "Robotics engineer" || jobTitle != "Engineer" || location != "Lisbon, Portugal" ||
		about != "Builds things." || imageURL != "https://media.example/ada.jpg" ||
		experience != `[{"company":"Example Works","title":"Engineer"}]` || education != `[{"school":"Example University"}]` {
		t.Fatalf("person = %q %q %q %q %q %q %s %s", fullName, headline, jobTitle, location, about, imageURL, experience, education)
	}
}
