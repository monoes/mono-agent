package peoplenodes

import (
	"context"
	"database/sql"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// A bare :memory: DSN gives each physical connection its own isolated
	// database (no shared-cache mode) — database/sql's pool can open more
	// than one connection under concurrent load, so a query can land on a
	// connection that never saw this function's own CREATE TABLE, failing
	// with "no such table" nondeterministically. Only manifests under
	// cross-package concurrency (go test ./...), never in isolation, which
	// is exactly why it went unnoticed until now.
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS people (
		id TEXT PRIMARY KEY,
		platform_username TEXT NOT NULL,
		platform TEXT NOT NULL,
		full_name TEXT,
		image_url TEXT,
		contact_details TEXT,
		website TEXT,
		content_count INTEGER,
		follower_count INTEGER,
		following_count INTEGER,
		introduction TEXT,
		is_verified INTEGER DEFAULT 0,
		category TEXT,
		job_title TEXT,
		profile_url TEXT,
		profile_id TEXT NOT NULL DEFAULT 'default',
		created_at DATETIME,
		updated_at DATETIME,
		UNIQUE(platform_username, platform, profile_id)
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func TestPeopleSaveNode_BasicUpsert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	node := &PeopleSaveNode{}
	SetGlobalPeopleDB(db)

	items := []workflow.Item{
		workflow.NewItem(map[string]interface{}{
			"profile_url": "https://www.linkedin.com/in/john-doe/",
			"full_name":   "John Doe",
			"job_title":   "CEO at Acme",
			"platform":    "linkedin",
		}),
		workflow.NewItem(map[string]interface{}{
			"profile_url": "https://www.linkedin.com/in/jane-smith/",
			"full_name":   "Jane Smith",
			"platform":    "linkedin",
		}),
	}

	outputs, err := node.Execute(context.Background(), workflow.NodeInput{Items: items}, map[string]interface{}{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(outputs) != 1 || outputs[0].Handle != "main" {
		t.Fatalf("expected 1 main output, got %+v", outputs)
	}
	if len(outputs[0].Items) != 2 {
		t.Fatalf("expected 2 saved items, got %d", len(outputs[0].Items))
	}

	// Verify DB rows.
	var count int
	db.QueryRow("SELECT COUNT(*) FROM people WHERE platform = 'LINKEDIN'").Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 people rows, got %d", count)
	}

	// Verify username extraction.
	var username, fullName string
	db.QueryRow("SELECT platform_username, full_name FROM people WHERE full_name = 'John Doe'").Scan(&username, &fullName)
	if username != "john-doe" {
		t.Fatalf("expected username 'john-doe', got %q", username)
	}
}

func TestPeopleSaveNode_HrefFallback(t *testing.T) {
	// Simulate what BrowserNode emits after normalizeBrowserItem: href-based item.
	db := setupTestDB(t)
	defer db.Close()
	SetGlobalPeopleDB(db)

	node := &PeopleSaveNode{}
	items := []workflow.Item{
		workflow.NewItem(map[string]interface{}{
			// normalizeBrowserItem maps href → profile_url/url
			"profile_url": "https://www.linkedin.com/in/alice-wonderland/",
			"url":         "https://www.linkedin.com/in/alice-wonderland/",
			"full_name":   "Alice Wonderland",
			"job_title":   "CTO at StartupCo",
			"platform":    "linkedin",
		}),
	}

	outputs, err := node.Execute(context.Background(), workflow.NodeInput{Items: items}, map[string]interface{}{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(outputs[0].Items) != 1 {
		t.Fatalf("expected 1 saved item, got %d", len(outputs[0].Items))
	}
	var jobTitle string
	db.QueryRow("SELECT job_title FROM people WHERE platform_username = 'alice-wonderland'").Scan(&jobTitle)
	if jobTitle != "CTO at StartupCo" {
		t.Errorf("job_title: got %q", jobTitle)
	}
}

func TestPeopleSaveNode_SkipsItemWithoutURL(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	SetGlobalPeopleDB(db)

	node := &PeopleSaveNode{}
	items := []workflow.Item{
		workflow.NewItem(map[string]interface{}{
			"full_name": "No URL Person",
			"platform":  "linkedin",
		}),
	}

	outputs, err := node.Execute(context.Background(), workflow.NodeInput{Items: items}, map[string]interface{}{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Item without URL is skipped — 0 saved items.
	if len(outputs[0].Items) != 0 {
		t.Fatalf("expected 0 saved items (no URL), got %d", len(outputs[0].Items))
	}
}

func TestPeopleSaveNode_PreservesVerifiedWhenKeyAbsent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	SetGlobalPeopleDB(db)

	node := &PeopleSaveNode{}

	// First save: is_verified=true (e.g. a profile scrape).
	_, err := node.Execute(context.Background(), workflow.NodeInput{Items: []workflow.Item{
		workflow.NewItem(map[string]interface{}{
			"profile_url": "https://www.linkedin.com/in/verified-vera/",
			"full_name":   "Verified Vera",
			"platform":    "linkedin",
			"is_verified": true,
		}),
	}}, map[string]interface{}{})
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}

	var verified int
	db.QueryRow("SELECT is_verified FROM people WHERE platform_username = 'verified-vera'").Scan(&verified)
	if verified != 1 {
		t.Fatalf("after first save: expected is_verified=1, got %d", verified)
	}

	// Second save: same person, no is_verified key (e.g. browser/LinkedIn extract).
	_, err = node.Execute(context.Background(), workflow.NodeInput{Items: []workflow.Item{
		workflow.NewItem(map[string]interface{}{
			"profile_url": "https://www.linkedin.com/in/verified-vera/",
			"full_name":   "Verified Vera",
			"platform":    "linkedin",
		}),
	}}, map[string]interface{}{})
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}

	db.QueryRow("SELECT is_verified FROM people WHERE platform_username = 'verified-vera'").Scan(&verified)
	if verified != 1 {
		t.Fatalf("after second save without is_verified key: expected preserved is_verified=1, got %d", verified)
	}
}

func TestPeopleSaveNode_ConfigPlatformOverride(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	SetGlobalPeopleDB(db)

	node := &PeopleSaveNode{}
	items := []workflow.Item{
		workflow.NewItem(map[string]interface{}{
			"profile_url": "https://www.linkedin.com/in/bob-builder/",
			"full_name":   "Bob Builder",
			// no platform in item — should use config override
		}),
	}

	_, err := node.Execute(context.Background(), workflow.NodeInput{Items: items}, map[string]interface{}{
		"platform": "linkedin",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var platform string
	db.QueryRow("SELECT platform FROM people WHERE platform_username = 'bob-builder'").Scan(&platform)
	if platform != "LINKEDIN" {
		t.Fatalf("expected platform LINKEDIN, got %q", platform)
	}
}

func TestPeopleSaveNode_CategoryAndIntroduction(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	SetGlobalPeopleDB(db)

	node := &PeopleSaveNode{}
	items := []workflow.Item{
		workflow.NewItem(map[string]interface{}{
			"profile_url": "https://www.linkedin.com/in/charlie-founder/",
			"name":        "Charlie Founder",
			"platform":    "linkedin",
			"pitch":       "Excited about your new startup!",
		}),
	}

	_, err := node.Execute(context.Background(), workflow.NodeInput{Items: items}, map[string]interface{}{
		"category":     "pending_approval",
		"introduction": "{{ $json.pitch }}",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var category, introduction, fullName string
	err = db.QueryRow("SELECT category, introduction, full_name FROM people WHERE platform_username = 'charlie-founder'").Scan(&category, &introduction, &fullName)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if category != "pending_approval" {
		t.Fatalf("expected category 'pending_approval', got %q", category)
	}
	if introduction != "Excited about your new startup!" {
		t.Fatalf("expected introduction 'Excited about your new startup!', got %q", introduction)
	}
	if fullName != "Charlie Founder" {
		t.Fatalf("expected full_name 'Charlie Founder', got %q", fullName)
	}
}
