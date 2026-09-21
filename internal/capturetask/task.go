// Package capturetask turns a capture envelope into a task on a board
// (GLU-03).
//
// The board is an existing one. mono-agent has no task system of its own —
// there is no monotask client in this repo, and the org runtime's issues
// live in monomind's store at <root>/.monomind/orgs/<org>-issues.json,
// which the mastermind-issues, mastermind-my-issues and
// mastermind-liveness skills read and the dashboard renders. So that is
// where a capture's task is written, in exactly that file's shape, rather
// than inventing a second place for tasks to hide.
//
// The file belongs to monomind. Every issue already in it is preserved
// byte for byte — the document is decoded as raw JSON, the new issue is
// appended, and the result is written through a temporary file and renamed,
// so a reader never sees a half-written board and a field this build has
// never heard of is never dropped.
package capturetask

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// issuesSuffix is monomind's own artifact suffix for an org's issue store
// (see orgdesign's orgArtifactSuffixes, which mirrors monomind's
// ORG_ARTIFACT_SUFFIXES).
const issuesSuffix = "-issues.json"

// Priorities are the values monomind's issue store accepts.
var Priorities = []string{"low", "medium", "high", "urgent"}

// DefaultPriority is what an unprioritised capture gets.
const DefaultPriority = "medium"

// statusTodo is the status a new issue starts in. monomind renamed "open"
// to "todo"; the skills migrate the old value on load.
const statusTodo = "todo"

// Options describes the task to create.
type Options struct {
	// Root is the project root holding .monomind/orgs.
	Root string
	// Org is the board — an org name under that root.
	Org string
	// EnvelopePath is the capture directory to attach.
	EnvelopePath string
	// Title defaults to the capture's title, then its URL.
	Title string
	// Description is added above the capture's own provenance block.
	Description string
	// Priority is one of Priorities; empty means DefaultPriority.
	Priority string
	// Assignee, Workspace and Parent map onto the store's assigneeId,
	// workspaceId and parentId.
	Assignee, Workspace, Parent string
	// Now supplies timestamps; nil means time.Now.
	Now func() time.Time
}

// Attachment is one file of the capture, in the shape
// mastermind-issue-detail's `attachments` action prints.
type Attachment struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	AddedAt   string `json:"added_at"`
}

// CaptureRef is the machine-readable link back to the envelope. It is an
// extra key on the issue: monomind's readers ignore what they do not know,
// and without it "which capture is this task about?" is answerable only by
// reading the description.
type CaptureRef struct {
	Path         string   `json:"path"`
	URL          string   `json:"url,omitempty"`
	CanonicalURL string   `json:"canonicalUrl,omitempty"`
	Title        string   `json:"title,omitempty"`
	CapturedAt   string   `json:"capturedAt,omitempty"`
	ContentHash  string   `json:"contentHash,omitempty"`
	Source       string   `json:"source,omitempty"`
	Collection   string   `json:"collection,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// Issue is one record in <org>-issues.json. The field names and the null
// defaults match what monomind's skills write, so an issue created here is
// indistinguishable from one created there.
type Issue struct {
	ID                string       `json:"id"`
	Title             string       `json:"title"`
	Description       string       `json:"description"`
	Status            string       `json:"status"`
	Priority          string       `json:"priority"`
	AssigneeID        *string      `json:"assigneeId"`
	AssigneeAgentID   *string      `json:"assigneeAgentId"`
	AssigneeUserID    *string      `json:"assigneeUserId"`
	ParentID          *string      `json:"parentId"`
	ProjectID         *string      `json:"projectId"`
	WorkspaceID       *string      `json:"workspaceId"`
	BlockedByIssueIDs []string     `json:"blockedByIssueIds"`
	CreatedAt         string       `json:"createdAt"`
	UpdatedAt         string       `json:"updatedAt"`
	Attachments       []Attachment `json:"attachments,omitempty"`
	Capture           *CaptureRef  `json:"capture,omitempty"`
}

// Result is what Create did.
type Result struct {
	Org        string `json:"org"`
	IssuesFile string `json:"issuesFile"`
	Issue      Issue  `json:"issue"`
}

// IssuesPath is the issue store for org under root.
func IssuesPath(root, org string) (string, error) {
	if !orgdesign.ValidOrgName(org) {
		return "", fmt.Errorf("invalid org name %q", org)
	}
	return filepath.Join(orgdesign.OrgsDir(root), org+issuesSuffix), nil
}

// Boards lists the orgs under root that can hold a task: every org with a
// config file, plus any that already has an issue store.
func Boards(root string) ([]string, error) {
	names, err := orgdesign.ListOrgNames(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	entries, err := os.ReadDir(orgdesign.OrgsDir(root))
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if name, ok := strings.CutSuffix(e.Name(), issuesSuffix); ok && orgdesign.ValidOrgName(name) {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// Create appends a task for one capture to an org's board.
func Create(opts Options) (*Result, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	if strings.TrimSpace(opts.Org) == "" {
		return nil, errors.New("capturetask: no board — pass --board <org>")
	}
	path, err := IssuesPath(opts.Root, opts.Org)
	if err != nil {
		return nil, err
	}
	priority := strings.ToLower(strings.TrimSpace(opts.Priority))
	if priority == "" {
		priority = DefaultPriority
	}
	if !contains(Priorities, priority) {
		return nil, fmt.Errorf("capturetask: priority %q must be one of %s", priority, strings.Join(Priorities, ", "))
	}

	env, err := readEnvelope(opts.EnvelopePath)
	if err != nil {
		return nil, err
	}

	doc, issues, err := loadBoard(path)
	if err != nil {
		return nil, err
	}
	if opts.Parent != "" && !hasIssue(issues, opts.Parent) {
		return nil, fmt.Errorf("capturetask: parent issue %q is not on board %q", opts.Parent, opts.Org)
	}

	ts := now().UTC().Format("2006-01-02T15:04:05Z")
	issue := Issue{
		ID:                nextID(issues, now()),
		Title:             firstNonEmpty(opts.Title, env.meta.Title, env.meta.DedupeURL(), filepath.Base(env.dir)),
		Description:       describe(opts.Description, env),
		Status:            statusTodo,
		Priority:          priority,
		AssigneeID:        optional(opts.Assignee),
		ParentID:          optional(opts.Parent),
		WorkspaceID:       optional(opts.Workspace),
		BlockedByIssueIDs: []string{},
		CreatedAt:         ts,
		UpdatedAt:         ts,
		Attachments:       env.attachments(ts),
		Capture:           env.ref(),
	}

	blob, err := json.Marshal(issue)
	if err != nil {
		return nil, fmt.Errorf("encode issue: %w", err)
	}
	issues = append(issues, blob)
	if err := saveBoard(path, doc, issues); err != nil {
		return nil, err
	}
	return &Result{Org: opts.Org, IssuesFile: path, Issue: issue}, nil
}

// loadBoard reads the issue store as raw JSON. Every existing issue is kept
// as the bytes it was written as: this process knows nothing about the
// fields monomind's own tooling adds, and re-encoding them through a Go
// struct is how they would be lost.
func loadBoard(path string) (map[string]json.RawMessage, []json.RawMessage, error) {
	blob, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read board %s: %w", path, err)
	}
	doc := map[string]json.RawMessage{}
	if err := json.Unmarshal(blob, &doc); err != nil {
		return nil, nil, fmt.Errorf("read board %s: %w", path, err)
	}
	var issues []json.RawMessage
	if raw, ok := doc["issues"]; ok {
		if err := json.Unmarshal(raw, &issues); err != nil {
			return nil, nil, fmt.Errorf("read board %s: issues is not a list: %w", path, err)
		}
	}
	return doc, issues, nil
}

// saveBoard writes the board through a temporary file in the same
// directory and renames it, so a crash cannot leave a truncated board.
func saveBoard(path string, doc map[string]json.RawMessage, issues []json.RawMessage) error {
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	list, err := json.Marshal(issues)
	if err != nil {
		return fmt.Errorf("encode issues: %w", err)
	}
	doc["issues"] = list
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode board: %w", err)
	}
	out = append(out, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-issues-*")
	if err != nil {
		return fmt.Errorf("stage board: %w", err)
	}
	staged := tmp.Name()
	defer os.Remove(staged)
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return fmt.Errorf("write board: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("flush board: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write board: %w", err)
	}
	if err := os.Chmod(staged, 0o600); err != nil {
		return fmt.Errorf("secure board: %w", err)
	}
	if err := os.Rename(staged, path); err != nil {
		return fmt.Errorf("publish board %s: %w", path, err)
	}
	return nil
}

// nextID mints an id in monomind's own form, issue-<epoch-millis>-NNN,
// stepping the counter past any id the board already holds — two captures
// filed in the same millisecond must not collide.
func nextID(issues []json.RawMessage, now time.Time) string {
	millis := now.UTC().UnixMilli()
	for n := 1; n < 1000; n++ {
		id := fmt.Sprintf("issue-%d-%03d", millis, n)
		if !hasIssue(issues, id) {
			return id
		}
	}
	return fmt.Sprintf("issue-%d-%d", millis, now.UnixNano()%1e6)
}

func hasIssue(issues []json.RawMessage, id string) bool {
	for _, raw := range issues {
		var probe struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.ID == id {
			return true
		}
	}
	return false
}

func optional(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	v := strings.TrimSpace(s)
	return &v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return "Untitled capture"
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// envelope is a capture directory, read once.
type envelope struct {
	dir   string
	meta  capture.Meta
	files []os.FileInfo
}

// readEnvelope loads a capture directory, refusing anything that is not one.
func readEnvelope(dir string) (*envelope, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("capturetask: no capture path given")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	meta, err := capture.ReadMeta(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("capturetask: %s is not a capture — it has no %s", abs, capture.MetaFile)
		}
		return nil, fmt.Errorf("capturetask: %s has an unreadable %s: %w", abs, capture.MetaFile, err)
	}
	env := &envelope{dir: abs, meta: *meta}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("read capture %s: %w", abs, err)
	}
	for _, e := range entries {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil {
			env.files = append(env.files, info)
		}
	}
	sort.Slice(env.files, func(i, j int) bool { return env.files[i].Name() < env.files[j].Name() })
	return env, nil
}

// attachments lists every file of the capture, so the task carries the
// capture rather than only mentioning it.
func (e *envelope) attachments(ts string) []Attachment {
	out := make([]Attachment, 0, len(e.files))
	for _, f := range e.files {
		out = append(out, Attachment{
			Type:      attachmentType(f.Name()),
			Name:      f.Name(),
			Path:      filepath.Join(e.dir, f.Name()),
			SizeBytes: f.Size(),
			AddedAt:   ts,
		})
	}
	return out
}

// attachmentType names the kind of artifact, for a reader listing them.
func attachmentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mhtml", ".html", ".htm":
		return "archive"
	case ".pdf":
		return "pdf"
	case ".md":
		return "markdown"
	case ".png", ".jpg", ".jpeg", ".webp":
		return "image"
	case ".json":
		return "metadata"
	case ".csv":
		return "table"
	}
	return "file"
}

func (e *envelope) ref() *CaptureRef {
	ref := &CaptureRef{
		Path:         e.dir,
		URL:          e.meta.URL,
		CanonicalURL: e.meta.CanonicalURL,
		Title:        e.meta.Title,
		CapturedAt:   e.meta.CapturedAt,
		ContentHash:  e.meta.ContentHash,
		Source:       e.meta.Source,
		Tags:         e.meta.Tags,
	}
	if e.meta.Collection != nil {
		ref.Collection = strings.TrimSpace(*e.meta.Collection)
	}
	return ref
}

// describe builds the issue body: what the person said, then where the
// capture came from — a block a reader can act on without this tool.
func describe(note string, e *envelope) string {
	var b strings.Builder
	if s := strings.TrimSpace(note); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	b.WriteString("## Capture\n\n")
	if url := e.meta.DedupeURL(); url != "" {
		fmt.Fprintf(&b, "- Source: %s\n", url)
	}
	if e.meta.Title != "" {
		fmt.Fprintf(&b, "- Title: %s\n", e.meta.Title)
	}
	if e.meta.Byline != nil && strings.TrimSpace(*e.meta.Byline) != "" {
		fmt.Fprintf(&b, "- Byline: %s\n", strings.TrimSpace(*e.meta.Byline))
	}
	if e.meta.CapturedAt != "" {
		fmt.Fprintf(&b, "- Captured: %s", e.meta.CapturedAt)
		if e.meta.Source != "" {
			fmt.Fprintf(&b, " (%s)", e.meta.Source)
		}
		b.WriteString("\n")
	}
	if len(e.meta.Tags) > 0 {
		fmt.Fprintf(&b, "- Tags: %s\n", strings.Join(e.meta.Tags, ", "))
	}
	fmt.Fprintf(&b, "- Envelope: %s\n", e.dir)
	names := make([]string, 0, len(e.files))
	for _, f := range e.files {
		names = append(names, f.Name())
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, "- Files: %s\n", strings.Join(names, ", "))
	}
	if note := e.meta.Note; note != nil && strings.TrimSpace(*note) != "" {
		fmt.Fprintf(&b, "\n> %s\n", strings.TrimSpace(*note))
	}
	return b.String()
}
