package capturetask

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

func strptr(s string) *string { return &s }

// seedCapture writes one envelope and returns its directory.
func seedCapture(t *testing.T, meta capture.Meta) string {
	t.Helper()
	w := &capture.Writer{Inbox: filepath.Join(t.TempDir(), "inbox")}
	res, err := w.Write(&capture.Envelope{
		Meta: meta,
		Artifacts: map[string]capture.Artifact{
			capture.ArtifactMHTML:    capture.Inline([]byte("archive bytes")),
			capture.ArtifactReadable: capture.Inline([]byte("# " + meta.Title + "\n\nbody\n")),
		},
	})
	if err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	return res.Path
}

// orgRoot makes a project root with one org config in it.
func orgRoot(t *testing.T, orgs ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".monomind", "orgs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, org := range orgs {
		if err := os.WriteFile(filepath.Join(dir, org+".json"),
			[]byte(`{"name":"`+org+`","version":2}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// board decodes an org's issue store.
func board(t *testing.T, root, org string) map[string]any {
	t.Helper()
	path, err := IssuesPath(root, org)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read board: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatalf("board is not valid JSON: %v", err)
	}
	return doc
}

func fixedNow() func() time.Time {
	at := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return at }
}

func TestCreateWritesMonomindsIssueShape(t *testing.T) {
	root := orgRoot(t, "acme")
	dir := seedCapture(t, capture.Meta{
		URL: "https://example.com/article", CanonicalURL: "https://example.com/article",
		Title: "An Article", CapturedAt: "2026-09-20T10:00:00Z", Source: "crawl",
		Byline: strptr("A. Writer"), Collection: strptr("research"), Tags: []string{"go"},
	})

	res, err := Create(Options{
		Root: root, Org: "acme", EnvelopePath: dir,
		Description: "Read this before the review.", Priority: "high",
		Assignee: "alice", Workspace: "ws-1", Now: fixedNow(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.IssuesFile != filepath.Join(root, ".monomind", "orgs", "acme-issues.json") {
		t.Errorf("wrote to %s", res.IssuesFile)
	}

	doc := board(t, root, "acme")
	issues, ok := doc["issues"].([]any)
	if !ok || len(issues) != 1 {
		t.Fatalf("board holds %v", doc["issues"])
	}
	issue := issues[0].(map[string]any)

	// The fields monomind's own skills read, in the form they read them.
	for key, want := range map[string]any{
		"status": "todo", "priority": "high", "title": "An Article",
		"assigneeId": "alice", "workspaceId": "ws-1",
	} {
		if issue[key] != want {
			t.Errorf("issue[%q] = %v, want %v", key, issue[key], want)
		}
	}
	for _, key := range []string{"id", "description", "createdAt", "updatedAt", "blockedByIssueIds"} {
		if _, ok := issue[key]; !ok {
			t.Errorf("issue is missing %q", key)
		}
	}
	for _, key := range []string{"parentId", "projectId", "assigneeAgentId", "assigneeUserId"} {
		if v, ok := issue[key]; !ok || v != nil {
			t.Errorf("issue[%q] = %v, want an explicit null", key, v)
		}
	}
	if !strings.HasPrefix(issue["id"].(string), "issue-") {
		t.Errorf("issue id = %v", issue["id"])
	}
	if desc := issue["description"].(string); !strings.Contains(desc, "Read this before the review.") ||
		!strings.Contains(desc, "https://example.com/article") || !strings.Contains(desc, dir) {
		t.Errorf("description does not carry the capture:\n%s", desc)
	}
}

func TestCreateAttachesEveryArtifact(t *testing.T) {
	root := orgRoot(t, "acme")
	dir := seedCapture(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})

	res, err := Create(Options{Root: root, Org: "acme", EnvelopePath: dir, Now: fixedNow()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	names := map[string]Attachment{}
	for _, a := range res.Issue.Attachments {
		names[a.Name] = a
	}
	for _, want := range []string{capture.MetaFile, capture.ArtifactMHTML, capture.ArtifactReadable} {
		a, ok := names[want]
		if !ok {
			t.Fatalf("attachments %v are missing %s", res.Issue.Attachments, want)
		}
		if a.SizeBytes <= 0 || a.AddedAt == "" || !strings.HasPrefix(a.Path, dir) {
			t.Errorf("attachment %s = %+v", want, a)
		}
	}
	if names[capture.ArtifactReadable].Type != "markdown" || names[capture.ArtifactMHTML].Type != "archive" {
		t.Errorf("attachment types = %+v", names)
	}
	if res.Issue.Capture == nil || res.Issue.Capture.Path != dir || res.Issue.Capture.ContentHash == "" {
		t.Errorf("issue.capture = %+v, want a machine-readable link back", res.Issue.Capture)
	}
}

func TestCreatePreservesIssuesItDidNotWrite(t *testing.T) {
	root := orgRoot(t, "acme")
	path, _ := IssuesPath(root, "acme")
	existing := `{
  "issues": [
    {"id": "issue-1", "title": "Older", "status": "in_progress", "somethingNewer": {"a": 1}}
  ],
  "boardSettings": {"columns": ["todo", "doing"]}
}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := seedCapture(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})
	if _, err := Create(Options{Root: root, Org: "acme", EnvelopePath: dir, Now: fixedNow()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	doc := board(t, root, "acme")
	if _, ok := doc["boardSettings"]; !ok {
		t.Error("a top-level key this tool does not know was dropped")
	}
	issues := doc["issues"].([]any)
	if len(issues) != 2 {
		t.Fatalf("board holds %d issues, want the old one plus the new", len(issues))
	}
	old := issues[0].(map[string]any)
	if old["id"] != "issue-1" || old["status"] != "in_progress" {
		t.Errorf("existing issue was rewritten: %+v", old)
	}
	if _, ok := old["somethingNewer"]; !ok {
		t.Error("a field this build does not know was dropped from an existing issue")
	}
}

func TestCreateIDsDoNotCollideWithinAMillisecond(t *testing.T) {
	root := orgRoot(t, "acme")
	dir := seedCapture(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})
	now := fixedNow()

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		res, err := Create(Options{Root: root, Org: "acme", EnvelopePath: dir, Now: now})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if seen[res.Issue.ID] {
			t.Fatalf("id %s was issued twice", res.Issue.ID)
		}
		seen[res.Issue.ID] = true
	}
}

func TestCreateValidatesItsInputs(t *testing.T) {
	root := orgRoot(t, "acme")
	dir := seedCapture(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})

	cases := map[string]Options{
		"no board":         {Root: root, EnvelopePath: dir},
		"bad org name":     {Root: root, Org: "../escape", EnvelopePath: dir},
		"bad priority":     {Root: root, Org: "acme", EnvelopePath: dir, Priority: "urgentish"},
		"missing capture":  {Root: root, Org: "acme", EnvelopePath: filepath.Join(root, "nope")},
		"unknown parent":   {Root: root, Org: "acme", EnvelopePath: dir, Parent: "issue-does-not-exist"},
		"no capture given": {Root: root, Org: "acme"},
	}
	for name, opts := range cases {
		opts.Now = fixedNow()
		if _, err := Create(opts); err == nil {
			t.Errorf("%s should have been refused", name)
		}
	}
}

func TestCreateAcceptsAKnownParent(t *testing.T) {
	root := orgRoot(t, "acme")
	dir := seedCapture(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})
	parent, err := Create(Options{Root: root, Org: "acme", EnvelopePath: dir, Now: fixedNow()})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(Options{Root: root, Org: "acme", EnvelopePath: dir, Parent: parent.Issue.ID, Now: fixedNow()})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if child.Issue.ParentID == nil || *child.Issue.ParentID != parent.Issue.ID {
		t.Errorf("child.parentId = %v", child.Issue.ParentID)
	}
}

func TestBoardsListsOrgsAndExistingStores(t *testing.T) {
	root := orgRoot(t, "acme", "beta")
	// An org whose config is gone but whose board is not.
	path := filepath.Join(root, ".monomind", "orgs", "legacy-issues.json")
	if err := os.WriteFile(path, []byte(`{"issues":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Boards(root)
	if err != nil {
		t.Fatalf("Boards: %v", err)
	}
	if strings.Join(got, ",") != "acme,beta,legacy" {
		t.Errorf("Boards = %v", got)
	}
	// An artifact file must not be mistaken for an org.
	if err := os.WriteFile(filepath.Join(root, ".monomind", "orgs", "acme-goals.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := Boards(root); strings.Join(got, ",") != "acme,beta,legacy" {
		t.Errorf("Boards = %v after an unrelated artifact landed", got)
	}
}

func TestBoardsOnAFreshRoot(t *testing.T) {
	got, err := Boards(t.TempDir())
	if err != nil {
		t.Fatalf("Boards: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Boards = %v, want none", got)
	}
}
