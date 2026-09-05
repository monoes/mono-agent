# Apply Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-taskdev")` (recommended) or `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. (`mastermind-taskdev` is not installed in this project — the controlling session acts as the task dispatcher directly via the Agent tool, per the prior five phase plans' precedent.)

**Goal:** Assemble everything needed for one job application (generated CV/cover letter + the posting open in a real browser window) with a structural, code-level guarantee that no path in this feature ever submits anything programmatically; an explicit manual "send" confirms the application was actually submitted.

**Architecture:** A new `internal/apply` package (`Prepare` reusing Phase 4's document generation, `OpenForApplication` launching a real visible browser via the Phase 4-established `go-rod` launcher pattern) plus CLI commands and one workflow node wrapping `Prepare` only (never the interactive browser-open step).

**Tech Stack:** `github.com/go-rod/rod` + `github.com/go-rod/rod/lib/launcher` (already dependencies, same pattern as Phase 4's `RenderPDF`/`crawl.go`). No new dependencies.

## Global Constraints

- Go toolchain at `~/.local/go/bin`, not on default PATH: `export PATH="$HOME/.local/go/bin:$PATH"` before any `go` command.
- **`internal/apply/browser.go` must never contain a click/submit call of any kind.** This is enforced by a literal source-grep test (Task 1) — not just a code-review expectation. If a future change to this file needs to add interaction beyond navigation, that test failing is the intended, correct signal to stop and reconsider, not something to adjust the test to permit.
- No new migration this phase — reuses Phase 1's existing `pending → applied` transition and Phase 4's vault document storage as-is.
- `apply`'s confirm-mode y/N prompt is a swappable package-level function var in the CLI file (mirroring Phase 4/5's `RenderPDFFunc`/`ExecFunc` pattern) so it's testable without real stdin.
- TDD: every behavior gets a failing test before its implementation.
- Commit after every task with a conventional-commits message ending with:
  ```
  Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
  ```

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/apply/apply.go` | `Prepare`. |
| `internal/apply/apply_test.go` | Tests, including document-reuse-on-second-call. |
| `internal/apply/browser.go` | `OpenForApplication`. |
| `internal/apply/browser_test.go` | Real-browser test + the structural no-click source-grep test. |
| `cmd/monoagentcli/application_apply.go` | `application apply`/`application send` commands. |
| `cmd/monoagentcli/application_apply_test.go` | CLI integration tests. |
| `cmd/monoagentcli/application.go` | Modified: register the new subcommands. |
| `internal/nodes/apply/prepare.go` / `prepare_schema.go` | `applications.prepare` workflow node (wraps `Prepare` only). |
| `internal/tools/schemagen/manifest.go` | Modified: one new manifest entry. |
| `internal/noderegistry/registry.go` | Modified: register the new node package. |

---

### Task 0: `Prepare`

**Files:**
- Create: `internal/apply/apply.go`
- Create: `internal/apply/apply_test.go`

**Interfaces:**
- Consumes: `documents.{GenerateDocument,DocTypeCV,DocTypeCoverLetter,CVData,CoverLetterData}` (Phase 4), `vault.ListDocuments` (Phase 3).
- Produces: `apply.Prepare(ctx context.Context, db *sql.DB, profileID, applicationID string, cvData documents.CVData, coverLetterData documents.CoverLetterData) (cvHTMLID, cvPDFID, letterHTMLID, letterPDFID string, err error)`. Consumed by Task 2 (CLI), Task 3 (node).

- [ ] **Step 1: Write the failing tests**

```go
// internal/apply/apply_test.go
package apply_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/apply"
	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/documents"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/vault"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "apply-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func fakeRenderPDF(t *testing.T) {
	t.Helper()
	orig := documents.RenderPDFFunc
	documents.RenderPDFFunc = func(ctx context.Context, html string) ([]byte, error) { return []byte("%PDF-fake"), nil }
	t.Cleanup(func() { documents.RenderPDFFunc = orig })
}

func TestPrepareGeneratesDocuments(t *testing.T) {
	fakeRenderPDF(t)
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	store := applications.NewStore(db.DB)
	app := &applications.Application{Kind: applications.KindJob, Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"}}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cvHTML, cvPDF, letterHTML, letterPDF, err := apply.Prepare(ctx, db.DB, "default", app.ID,
		documents.CVData{Name: "Jane Doe"}, documents.CoverLetterData{SenderName: "Jane Doe"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if cvHTML == "" || cvPDF == "" || letterHTML == "" || letterPDF == "" {
		t.Fatalf("expected all 4 document ids to be set, got %q %q %q %q", cvHTML, cvPDF, letterHTML, letterPDF)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 4 {
		t.Fatalf("expected 4 vault documents (cv html+pdf, letter html+pdf), got %d", len(docs))
	}
}

func TestPrepareReusesExistingDocumentsOnSecondCall(t *testing.T) {
	fakeRenderPDF(t)
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	store := applications.NewStore(db.DB)
	app := &applications.Application{Kind: applications.KindJob, Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"}}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cvData := documents.CVData{Name: "Jane Doe"}
	letterData := documents.CoverLetterData{SenderName: "Jane Doe"}

	id1a, id1b, id1c, id1d, err := apply.Prepare(ctx, db.DB, "default", app.ID, cvData, letterData)
	if err != nil {
		t.Fatalf("Prepare (1st): %v", err)
	}
	id2a, id2b, id2c, id2d, err := apply.Prepare(ctx, db.DB, "default", app.ID, cvData, letterData)
	if err != nil {
		t.Fatalf("Prepare (2nd): %v", err)
	}
	if id1a != id2a || id1b != id2b || id1c != id2c || id1d != id2d {
		t.Fatalf("expected the second Prepare call to reuse the same document ids, got (%s,%s,%s,%s) vs (%s,%s,%s,%s)",
			id1a, id1b, id1c, id1d, id2a, id2b, id2c, id2d)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 4 {
		t.Fatalf("expected still only 4 vault documents after a second Prepare call, got %d", len(docs))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/apply/... -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write apply.go**

```go
// internal/apply/apply.go

// Package apply assembles everything needed to complete one job
// application by hand (generated documents + the posting open in a
// browser) — see docs/mastermind/specs/2026-09-05-apply-automation-design.md
// for why this phase deliberately does not auto-fill or auto-submit
// anything.
package apply

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/monoes/mono-agent/internal/documents"
	"github.com/monoes/mono-agent/internal/vault"
)

// Prepare ensures applicationID has a generated CV and cover letter in
// the vault, generating them now if none exist yet for this application
// (checked via vault.ListDocuments filtered by ApplicationID) — calling
// Prepare twice on the same application reuses the existing documents
// rather than creating duplicates.
func Prepare(ctx context.Context, db *sql.DB, profileID, applicationID string, cvData documents.CVData, coverLetterData documents.CoverLetterData) (cvHTMLID, cvPDFID, letterHTMLID, letterPDFID string, err error) {
	existing, err := vault.ListDocuments(ctx, db, profileID)
	if err != nil {
		return "", "", "", "", fmt.Errorf("apply.Prepare: listing existing documents: %w", err)
	}

	// A generated document's Source is always "generated" (set by
	// documents.GenerateDocument via vault.RegisterDocument) — Phase 3's
	// manually-uploaded profile documents use Source "upload" and are
	// never mistaken for a prior CV/cover-letter generation here.
	var forApp []vault.DocumentEntry
	for _, d := range existing {
		if d.ApplicationID == applicationID && d.Source == "generated" {
			forApp = append(forApp, d)
		}
	}

	// Two documents (CV, cover letter) each produce two vault entries
	// (HTML, PDF) in creation order: cv-html, cv-pdf, letter-html,
	// letter-pdf (documents.GenerateDocument always registers HTML then
	// PDF; Prepare always generates CV then cover letter below) — so 4
	// existing entries for this application means both were already
	// generated by a prior Prepare call.
	if len(forApp) >= 4 {
		// ListDocuments orders newest-first; the most recent 4 are this
		// application's most recent CV+letter pair in reverse creation
		// order (letter-pdf, letter-html, cv-pdf, cv-html).
		return forApp[3].ID, forApp[2].ID, forApp[1].ID, forApp[0].ID, nil
	}

	cvHTMLID, cvPDFID, err = documents.GenerateDocument(ctx, db, profileID, applicationID, documents.DocTypeCV, cvData)
	if err != nil {
		return "", "", "", "", fmt.Errorf("apply.Prepare: generating cv: %w", err)
	}
	letterHTMLID, letterPDFID, err = documents.GenerateDocument(ctx, db, profileID, applicationID, documents.DocTypeCoverLetter, coverLetterData)
	if err != nil {
		return cvHTMLID, cvPDFID, "", "", fmt.Errorf("apply.Prepare: generating cover letter: %w", err)
	}
	return cvHTMLID, cvPDFID, letterHTMLID, letterPDFID, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/apply/... -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/apply/apply.go internal/apply/apply_test.go
git commit -m "$(cat <<'EOF'
feat(apply): add Prepare

Generates (or reuses, on a repeat call) an application's CV and cover
letter via documents.GenerateDocument, scoped by vault_documents'
application_id link and a "generated" source check.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 1: `OpenForApplication` + the structural no-click test

**Files:**
- Create: `internal/apply/browser.go`
- Create: `internal/apply/browser_test.go`

**Interfaces:**
- Consumes: `github.com/go-rod/rod`, `github.com/go-rod/rod/lib/launcher`, `github.com/go-rod/rod/lib/proto` (existing dependencies).
- Produces: `apply.OpenForApplication(ctx context.Context, jobURL string) error`. Consumed by Task 2 (CLI).

- [ ] **Step 1: Write the failing tests**

```go
// internal/apply/browser_test.go
package apply_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apply"
)

func TestOpenForApplicationLaunchesBrowser(t *testing.T) {
	err := apply.OpenForApplication(context.Background(), "about:blank")
	if err != nil {
		t.Fatalf("OpenForApplication: %v (requires a headless Chrome/Chromium binary reachable by go-rod's launcher — if none is available in this environment, this is an environmental limitation, not a code defect; report it as such rather than treating it as a logic bug)", err)
	}
}

// TestBrowserFileNeverClicksAnything is a literal source-grep, not a
// behavioral test: it asserts internal/apply/browser.go's source text
// contains none of the substrings a click/submit call would use. This is
// the mechanical enforcement of this phase's core safety invariant — see
// docs/mastermind/specs/2026-09-05-apply-automation-design.md. If this
// test ever needs to change, that is a deliberate, reviewed decision to
// weaken the invariant, not a test to "fix" in passing.
func TestBrowserFileNeverClicksAnything(t *testing.T) {
	src, err := os.ReadFile("browser.go")
	if err != nil {
		t.Fatalf("reading browser.go: %v", err)
	}
	forbidden := []string{"Click", "MustClick", ".Submit", "Keyboard.Type", "MustSubmit"}
	for _, f := range forbidden {
		if strings.Contains(string(src), f) {
			t.Fatalf("internal/apply/browser.go must never call anything resembling %q — found it in the source. This file's only job is to navigate to a URL and leave the window open for a human.", f)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/apply/... -run TestOpenForApplication -v`
Expected: FAIL with "undefined: apply.OpenForApplication" (compile error). The grep test also fails to compile for the same reason (same package, same missing file).

- [ ] **Step 3: Write browser.go**

```go
// internal/apply/browser.go
package apply

import (
	"context"
	"fmt"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// OpenForApplication launches a real, VISIBLE browser window at jobURL,
// for a human to complete the application by hand. This function
// contains no interaction beyond navigation — a companion test in this
// package mechanically enforces that this file never grows a
// form-interaction call. The browser is deliberately NOT closed before
// returning (unlike documents.RenderPDF's throwaway headless instance) —
// it stays open for the user to use.
func OpenForApplication(ctx context.Context, jobURL string) error {
	launchURL, err := launcher.New().Headless(false).Launch()
	if err != nil {
		return fmt.Errorf("apply.OpenForApplication: launch browser: %w", err)
	}

	browser := rod.New().ControlURL(launchURL).Context(ctx)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("apply.OpenForApplication: connect to browser: %w", err)
	}

	if _, err := browser.Page(proto.TargetCreateTarget{URL: jobURL}); err != nil {
		return fmt.Errorf("apply.OpenForApplication: open page: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/apply/... -v 2>&1 | tail -20`
Expected: PASS — `TestBrowserFileNeverClicksAnything` passes unconditionally (pure source inspection). `TestOpenForApplicationLaunchesBrowser` passes if a headless Chrome/Chromium binary is reachable (already confirmed available and cached in this environment from Phase 4 — should be fast, no re-download). Follow the same non-workaround guidance as Phase 4's `pdf_test.go` if it genuinely fails for that specific environmental reason.

- [ ] **Step 5: Commit**

```bash
git add internal/apply/browser.go internal/apply/browser_test.go
git commit -m "$(cat <<'EOF'
feat(apply): add OpenForApplication with a structural no-click guarantee

Launches a real, visible browser at the job URL and leaves it open for
the user — never closes it, never clicks anything. Enforced by a literal
source-grep test, not just code review, so the invariant can't silently
regress later.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 2: CLI commands

**Files:**
- Create: `cmd/monoagentcli/application_apply.go`
- Create: `cmd/monoagentcli/application_apply_test.go`
- Modify: `cmd/monoagentcli/application.go`

**Interfaces:**
- Consumes: `apply.{Prepare,OpenForApplication}` (Tasks 0-1), `applications.{Store,StatusApplied}` (Phase 1), `documents.{CVData,CoverLetterData}` (Phase 4); `initDB`, `errInvalidInput` (existing CLI conventions).
- Produces: `newApplicationApplyCmd`, `newApplicationSendCmd`, added to `newApplicationCmd`'s subcommand list; `confirmPromptFunc` (swappable var). No other task depends on this.

- [ ] **Step 1: Write the failing CLI tests**

```go
// cmd/monoagentcli/application_apply_test.go
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/documents"
)

func runApplicationApplyCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newApplicationCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func writeDataFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplicationApplyAutoModeSkipsPrompt(t *testing.T) {
	origPDF := documents.RenderPDFFunc
	documents.RenderPDFFunc = func(ctx context.Context, html string) ([]byte, error) { return []byte("%PDF-fake"), nil }
	t.Cleanup(func() { documents.RenderPDFFunc = origPDF })

	origPrompt := confirmPromptFunc
	confirmPromptFunc = func(string) bool {
		t.Fatal("auto mode must never call the confirmation prompt")
		return false
	}
	t.Cleanup(func() { confirmPromptFunc = origPrompt })

	dbPath := newApplicationCLITestDB(t)
	addOut, err := runApplicationApplyCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "about:blank")
	if err != nil {
		t.Fatalf("application add: %v (%s)", err, addOut)
	}
	var id string
	for _, tok := range strings.Split(addOut, `"`) {
		if len(tok) == 36 && strings.Count(tok, "-") == 4 {
			id = tok
			break
		}
	}

	cvFile := writeDataFile(t, `{"name":"Jane Doe"}`)
	letterFile := writeDataFile(t, `{"senderName":"Jane Doe"}`)
	out, err := runApplicationApplyCmd(t, dbPath, "apply", id, "--mode", "auto", "--cv-data-file", cvFile, "--cover-letter-data-file", letterFile)
	if err != nil {
		t.Fatalf("application apply: %v (%s)", err, out)
	}
}

func TestApplicationApplyConfirmModeDeclined(t *testing.T) {
	origPrompt := confirmPromptFunc
	confirmPromptFunc = func(string) bool { return false }
	t.Cleanup(func() { confirmPromptFunc = origPrompt })

	dbPath := newApplicationCLITestDB(t)
	addOut, err := runApplicationApplyCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "about:blank")
	if err != nil {
		t.Fatalf("application add: %v (%s)", err, addOut)
	}
	var id string
	for _, tok := range strings.Split(addOut, `"`) {
		if len(tok) == 36 && strings.Count(tok, "-") == 4 {
			id = tok
			break
		}
	}

	cvFile := writeDataFile(t, `{"name":"Jane Doe"}`)
	letterFile := writeDataFile(t, `{"senderName":"Jane Doe"}`)
	// --mode confirm is explicit here because runApplicationApplyCmd's cfg
	// always sets JSONOutput: true, which the command's own default-mode
	// logic treats as "auto" — without this flag the confirm branch (and
	// confirmPromptFunc) would never be reached at all.
	out, err := runApplicationApplyCmd(t, dbPath, "apply", id, "--mode", "confirm", "--cv-data-file", cvFile, "--cover-letter-data-file", letterFile)
	if err != nil {
		t.Fatalf("application apply (declined): %v (%s)", err, out)
	}
	if !strings.Contains(out, "cancelled") && !strings.Contains(out, "Cancelled") {
		t.Fatalf("expected a cancellation message when the confirm prompt is declined, got: %s", out)
	}
}

func TestApplicationSendTransitionsToApplied(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	addOut, err := runApplicationApplyCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "about:blank")
	if err != nil {
		t.Fatalf("application add: %v (%s)", err, addOut)
	}
	var id string
	for _, tok := range strings.Split(addOut, `"`) {
		if len(tok) == 36 && strings.Count(tok, "-") == 4 {
			id = tok
			break
		}
	}

	sendOut, err := runApplicationApplyCmd(t, dbPath, "send", id)
	if err != nil {
		t.Fatalf("application send: %v (%s)", err, sendOut)
	}

	getOut, err := runApplicationApplyCmd(t, dbPath, "get", id)
	if err != nil {
		t.Fatalf("application get: %v", err)
	}
	if !strings.Contains(getOut, "applied") {
		t.Fatalf("expected status applied after send, got: %s", getOut)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run 'TestApplicationApply|TestApplicationSend' -v`
Expected: FAIL — `undefined: confirmPromptFunc` (compile error) and unknown "apply"/"send" subcommands.

- [ ] **Step 3: Write the commands**

```go
// cmd/monoagentcli/application_apply.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/monoes/mono-agent/internal/apply"
	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/documents"

	"github.com/spf13/cobra"
)

// confirmPromptFunc asks the user a y/N question on stdin/stdout, real by
// default; swappable for tests. Returns true only on an explicit "y"/"yes"
// (case-insensitive) — any other input, including a read error or EOF,
// is treated as "no" (the safe default for an action that opens a real
// browser and starts an application flow).
var confirmPromptFunc = func(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func newApplicationApplyCmd(cfg *globalConfig) *cobra.Command {
	var mode, cvDataFile, letterDataFile string
	cmd := &cobra.Command{
		Use:   "apply <id>",
		Short: "Prepare an application's documents and open the posting in a browser",
		Long: "Generates (or reuses) the CV and cover letter for this application, then opens the job\n" +
			"posting in a real browser window for you to complete and submit yourself — this command\n" +
			"never fills in or submits the form on your behalf. Use `application send` afterward to\n" +
			"record that you sent it.",
		Args:    cobra.ExactArgs(1),
		Example: `  monoagentcli application apply 1c2e... --cv-data-file cv.json --cover-letter-data-file letter.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			store := applications.NewStore(db.DB)
			app, err := store.Get(cmd.Context(), cfg.ProfileID, args[0])
			if err != nil {
				return fmt.Errorf("getting application: %w", err)
			}
			if app.Kind != applications.KindJob {
				return errInvalidInput("application apply currently supports job-kind applications only, got %q", app.Kind)
			}

			effectiveMode := mode
			if effectiveMode == "" {
				if cfg.JSONOutput {
					effectiveMode = "auto"
				} else {
					effectiveMode = "confirm"
				}
			}
			if effectiveMode != "auto" && effectiveMode != "confirm" {
				return errInvalidInput("--mode must be \"auto\" or \"confirm\", got %q", effectiveMode)
			}

			if effectiveMode == "confirm" {
				prompt := fmt.Sprintf("About to prepare and open %s — %s (%s). Continue?", app.Job.Company, app.Job.Title, app.Job.URL)
				if !confirmPromptFunc(prompt) {
					fmt.Fprintln(cmd.OutOrStdout(), "Cancelled.")
					return nil
				}
			}

			var cvData documents.CVData
			if cvDataFile != "" {
				raw, err := os.ReadFile(cvDataFile)
				if err != nil {
					return fmt.Errorf("reading --cv-data-file: %w", err)
				}
				if err := json.Unmarshal(raw, &cvData); err != nil {
					return errInvalidInput("parsing --cv-data-file: %v", err)
				}
			}
			var letterData documents.CoverLetterData
			if letterDataFile != "" {
				raw, err := os.ReadFile(letterDataFile)
				if err != nil {
					return fmt.Errorf("reading --cover-letter-data-file: %w", err)
				}
				if err := json.Unmarshal(raw, &letterData); err != nil {
					return errInvalidInput("parsing --cover-letter-data-file: %v", err)
				}
			}

			cvHTML, cvPDF, letterHTML, letterPDF, err := apply.Prepare(cmd.Context(), db.DB, cfg.ProfileID, app.ID, cvData, letterData)
			if err != nil {
				return fmt.Errorf("preparing documents: %w", err)
			}

			if err := apply.OpenForApplication(cmd.Context(), app.Job.URL); err != nil {
				return fmt.Errorf("opening browser: %w", err)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{
					"cv_html_document_id": cvHTML, "cv_pdf_document_id": cvPDF,
					"cover_letter_html_document_id": letterHTML, "cover_letter_pdf_document_id": letterPDF,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Opened %s. Documents ready — cv: %s / %s, cover letter: %s / %s\nRun `monoagentcli application send %s` once you've submitted it.\n",
				app.Job.URL, cvHTML, cvPDF, letterHTML, letterPDF, app.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "confirm (default in text mode) or auto (default in --json mode)")
	cmd.Flags().StringVar(&cvDataFile, "cv-data-file", "", "Path to a JSON file matching documents.CVData's fields")
	cmd.Flags().StringVar(&letterDataFile, "cover-letter-data-file", "", "Path to a JSON file matching documents.CoverLetterData's fields")
	return cmd
}

func newApplicationSendCmd(cfg *globalConfig) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "send <id>",
		Short: "Record that you submitted this application yourself",
		Long:  "This is the only way an application's status becomes \"applied\" — mono-agent never submits a form on your behalf; this command is how you tell it you did.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			store := applications.NewStore(db.DB)
			if err := store.SetStatus(cmd.Context(), cfg.ProfileID, args[0], applications.StatusApplied, applications.ActorUser, note); err != nil {
				return fmt.Errorf("recording send: %w", err)
			}
			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"id": args[0], "status": "applied"})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Recorded %s as applied.\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "Optional note recorded in the transition ledger")
	return cmd
}
```

- [ ] **Step 4: Register the subcommands**

In `cmd/monoagentcli/application.go`, add to `newApplicationCmd`'s `cmd.AddCommand(...)` list:

```go
	cmd.AddCommand(
		newApplicationAddCmd(cfg),
		newApplicationListCmd(cfg),
		newApplicationGetCmd(cfg),
		newApplicationStatusCmd(cfg),
		newApplicationTagCmd(cfg),
		newApplicationDiscoverCmd(cfg),
		newApplicationEvaluateCmd(cfg),
		newApplicationEvaluatePendingCmd(cfg),
		newApplicationApplyCmd(cfg),
		newApplicationSendCmd(cfg),
	)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run 'TestApplicationApply|TestApplicationSend' -v 2>&1 | tail -30`
Expected: PASS (3 tests). Note `TestApplicationApplyAutoModeSkipsPrompt` requires a reachable browser (same environmental note as Phase 4/Task 1 above — should work given the cached Chromium).

- [ ] **Step 6: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing.

- [ ] **Step 7: Commit**

```bash
git add cmd/monoagentcli/application_apply.go cmd/monoagentcli/application_apply_test.go cmd/monoagentcli/application.go
git commit -m "$(cat <<'EOF'
feat(cli): add `monoagentcli application apply`/`send`

apply prepares documents and opens the posting in a browser (confirm
mode by default in text output, auto by default for --json callers);
send is the only path that ever marks an application "applied".

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 3: Workflow node `applications.prepare`

**Files:**
- Create: `internal/nodes/apply/prepare.go`
- Create: `internal/nodes/apply/prepare_schema.go`
- Create: `internal/nodes/apply/prepare_test.go`
- Modify: `internal/tools/schemagen/manifest.go`
- Modify: `internal/noderegistry/registry.go`
- Create (generated, not hand-edited): `internal/workflow/schemas/applications.prepare.json`

**Interfaces:**
- Consumes: `apply.Prepare` (Task 0) — deliberately NOT `apply.OpenForApplication`, since a workflow node runs unattended and should never pop open an interactive browser window as an automated side effect; `workflow.{NodeExecutor,NodeInput,NodeOutput,Item,NewItem,NodeTypeRegistry}` (existing).
- Produces: node type `applications.prepare` registered in the global registry. No other task in this plan depends on it.

- [ ] **Step 1: Write the failing test**

```go
// internal/nodes/apply/prepare_test.go
package applynodes_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	applynodes "github.com/monoes/mono-agent/internal/nodes/apply"
	"github.com/monoes/mono-agent/internal/documents"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "apply-node-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPrepareNodeGeneratesDocuments(t *testing.T) {
	origPDF := documents.RenderPDFFunc
	documents.RenderPDFFunc = func(ctx context.Context, html string) ([]byte, error) { return []byte("%PDF-fake"), nil }
	t.Cleanup(func() { documents.RenderPDFFunc = origPDF })

	db := newTestDB(t)
	applynodes.SetGlobalDB(db.DB)
	store := applications.NewStore(db.DB)
	app := &applications.Application{Kind: applications.KindJob, Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"}}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	node := &applynodes.PrepareNode{}
	if node.Type() != "applications.prepare" {
		t.Fatalf("expected type applications.prepare, got %q", node.Type())
	}
	config := map[string]interface{}{
		"application_id":     app.ID,
		"cv_data":            map[string]interface{}{"name": "Jane Doe"},
		"cover_letter_data":  map[string]interface{}{"senderName": "Jane Doe"},
	}
	outputs, err := node.Execute(context.Background(), workflow.NodeInput{}, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outputs[0].Items[0].JSON["cv_pdf_document_id"] == "" {
		t.Fatal("expected cv_pdf_document_id in output")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/nodes/apply/... -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write the node**

```go
// internal/nodes/apply/prepare.go

// Package applynodes exposes internal/apply as a workflow node type:
// applications.prepare. Deliberately does not expose OpenForApplication
// as a node — an unattended workflow should never pop open an interactive
// browser window as an automated side effect.
package applynodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/apply"
	"github.com/monoes/mono-agent/internal/documents"
	"github.com/monoes/mono-agent/internal/workflow"
)

var globalDB *sql.DB

// SetGlobalDB wires the shared SQLite connection into this package's node(s).
func SetGlobalDB(db *sql.DB) {
	globalDB = db
}

// RegisterAll registers applications.prepare into the registry.
func RegisterAll(r *workflow.NodeTypeRegistry, db *sql.DB) {
	SetGlobalDB(db)
	r.Register("applications.prepare", func() workflow.NodeExecutor { return &PrepareNode{} })
}

// PrepareNode generates (or reuses) an application's CV and cover letter
// via apply.Prepare.
// Type: "applications.prepare"
type PrepareNode struct{}

func (n *PrepareNode) Type() string { return "applications.prepare" }

func (n *PrepareNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	if globalDB == nil {
		return nil, fmt.Errorf("applications.prepare: database not available (call SetGlobalDB at startup)")
	}
	applicationID, _ := config["application_id"].(string)
	if applicationID == "" {
		return nil, fmt.Errorf("applications.prepare: config \"application_id\" is required")
	}
	profileID, _ := config["profile_id"].(string)
	if profileID == "" {
		profileID = "default"
	}

	cvRaw, _ := config["cv_data"].(map[string]interface{})
	letterRaw, _ := config["cover_letter_data"].(map[string]interface{})

	var cvData documents.CVData
	if cvJSON, err := json.Marshal(cvRaw); err == nil {
		_ = json.Unmarshal(cvJSON, &cvData)
	}
	var letterData documents.CoverLetterData
	if letterJSON, err := json.Marshal(letterRaw); err == nil {
		_ = json.Unmarshal(letterJSON, &letterData)
	}

	cvHTML, cvPDF, letterHTML, letterPDF, err := apply.Prepare(ctx, globalDB, profileID, applicationID, cvData, letterData)
	if err != nil {
		return nil, fmt.Errorf("applications.prepare: %w", err)
	}

	out := map[string]interface{}{
		"application_id": applicationID,
		"cv_html_document_id": cvHTML, "cv_pdf_document_id": cvPDF,
		"cover_letter_html_document_id": letterHTML, "cover_letter_pdf_document_id": letterPDF,
	}
	return []workflow.NodeOutput{{Handle: "main", Items: []workflow.Item{workflow.NewItem(out)}}}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/nodes/apply/... -v`
Expected: PASS.

- [ ] **Step 5: Write the schema struct**

```go
// internal/nodes/apply/prepare_schema.go
package applynodes

// PrepareNodeSchema documents the config keys PrepareNode.Execute reads
// out of its map[string]interface{} config.
type PrepareNodeSchema struct {
	ApplicationID   string `json:"application_id" schema:"label=Application ID,type=text,required,help=The job application to prepare documents for."`
	CVData          string `json:"cv_data" schema:"label=CV Data,type=code,language=json,required,help=A JSON object matching documents.CVData's fields."`
	CoverLetterData string `json:"cover_letter_data" schema:"label=Cover Letter Data,type=code,language=json,required,help=A JSON object matching documents.CoverLetterData's fields."`
	ProfileID       string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns this application."`
}
```

- [ ] **Step 6: Register in the schemagen manifest**

In `internal/tools/schemagen/manifest.go`, add a new section after `applications.evaluate`:

```go
	// --- applications.prepare (apply) ---
	{NodeType: "applications.prepare", GoFile: "internal/nodes/apply/prepare_schema.go", StructName: "PrepareNodeSchema"},
```

- [ ] **Step 7: Generate the schema and register the node package**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go run ./cmd/schemagen`
Expected: writes `internal/workflow/schemas/applications.prepare.json`. Double-check every `help=` string above for a literal comma before running — none are present here.

In `internal/noderegistry/registry.go`, add the import (alphabetically) and register call:

```go
	applynodes "github.com/monoes/mono-agent/internal/nodes/apply"
```

```go
	applynodes.RegisterAll(registry, db)
```

- [ ] **Step 8: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing. Completes Phase 6 (backend).

- [ ] **Step 9: Commit**

```bash
git add internal/nodes/apply/ internal/tools/schemagen/manifest.go internal/noderegistry/registry.go internal/workflow/schemas/applications.prepare.json
git commit -m "$(cat <<'EOF'
feat(apply): add applications.prepare workflow node

Wraps apply.Prepare only (never OpenForApplication) — an unattended
workflow should never pop open an interactive browser window. Completes
Phase 6 backend (Apply Automation).

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

## Self-Review

**1. Spec coverage:**
- `Prepare` (document assembly, idempotent reuse) → Task 0. ✅
- `OpenForApplication` (visible browser, no interaction) → Task 1, with a mechanical (not just reviewed) enforcement test. ✅
- CLI `apply`/`send`, confirm-vs-auto default behavior → Task 2. ✅
- `send` as the sole path to `applied` status → Task 2, tested explicitly. ✅
- Workflow node, deliberately excluding the interactive browser step → Task 3. ✅
- Adaptive form auto-fill, tender staging, content-generation-from-posting, Wails GUI — all explicitly Out of Scope in the spec, none silently implemented here. ✅

**2. Placeholder scan:** No "TBD"/"TODO". Every step has complete code.

**3. Type consistency:** `apply.Prepare`'s signature (`ctx, db, profileID, applicationID string, cvData documents.CVData, coverLetterData documents.CoverLetterData) (cvHTMLID, cvPDFID, letterHTMLID, letterPDFID string, err error)`, introduced in Task 0, is called identically in Task 2's CLI and Task 3's node. `apply.OpenForApplication(ctx, jobURL string) error` (Task 1) is called identically in Task 2. `confirmPromptFunc`'s swappable-var pattern (Task 2) matches the established `RenderPDFFunc`/`ExecFunc` convention exactly (same shape: a package-level var initialized to the real implementation).
