package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/monoes/mono-agent/internal/ai"
	aichat "github.com/monoes/mono-agent/internal/ai/chat"
	"github.com/monoes/mono-agent/internal/capturedocs"
	"github.com/monoes/mono-agent/internal/connections"
	"github.com/monoes/mono-agent/internal/docscan"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/nodes"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/vault"
	"github.com/monoes/mono-agent/internal/workflow"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	_ "modernc.org/sqlite"
)

// App holds application state bound to the Wails runtime.
type App struct {
	ctx         context.Context
	db          *sql.DB
	dbPath      string
	logs        []LogEntry
	logsMu      sync.Mutex
	connMgr     *connections.Manager
	aiStore     *ai.AIStore
	chatService *aichat.ChatService
	chatSup     *chatSupervisor // new conversation/turn/event supervisor; see app_chat.go
	wfStore     *workflow.HybridWorkflowStore

	runningMu      sync.Mutex
	runningCmds    map[string]*exec.Cmd // workflowID / "action:<id>" / "noderun:<id>" → running subprocess
	nodeRunCounter atomic.Int64         // source of RunNode run ids (NodeRunResult.run_id)

	chatCancels sync.Map // workflowID → *cancelHandle for in-flight AI chat streams

	activeProfileIDPtr atomic.Pointer[string] // currently selected profile; access via get/setActiveProfileID (read/written across Wails goroutines)
	ready              atomic.Bool            // set once startup()'s synchronous setup has finished; see IsReady

	orgWatchMu sync.Mutex
	orgWatcher *orgdesign.Watcher // polls the active profile's .monomind/orgs/ dir; see restartOrgWatcher

	docWatchMu sync.Mutex
	docWatcher *docscan.Watcher     // polls the active profile's whole folder (minus .monomind/) for document changes; see restartDocumentWatcher
	capWatcher *capturedocs.Watcher // polls the active profile's browser-capture inbox; see restartDocumentWatcher
}

// cancelHandle wraps a stream's cancel func in a pointer so it has a comparable
// identity for sync.Map.CompareAndDelete.
type cancelHandle struct{ cancel context.CancelFunc }

// NewApp creates the App instance.
func NewApp() *App {
	home, _ := os.UserHomeDir()
	return &App{
		dbPath:      filepath.Join(home, ".monoagent", "monoagent.db"),
		logs:        make([]LogEntry, 0, 200),
		runningCmds: make(map[string]*exec.Cmd),
	}
}

// getActiveProfileID returns the currently selected profile id. Wails dispatches
// bound methods on independent goroutines, so this is read concurrently with
// SwitchProfile writes — the atomic makes that access race-free. Defaults to
// "default" before startup sets it.
func (a *App) getActiveProfileID() string {
	if p := a.activeProfileIDPtr.Load(); p != nil {
		return *p
	}
	return "default"
}

// setActiveProfileID atomically updates the selected profile id.
func (a *App) setActiveProfileID(id string) {
	a.activeProfileIDPtr.Store(&id)
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// Installed automation packages, before anything reads actions or forms.
	if _, err := nodes.BootAutomations(""); err != nil {
		runtime.LogWarningf(ctx, "automations: %v (using the built-in action set)", err)
	}
	// A first launch on a new machine has no ~/.monoagent yet; SQLite can't
	// create the database file inside a folder that doesn't exist (the CLI's
	// initDB makes it the same way).
	if err := os.MkdirAll(filepath.Dir(a.dbPath), 0o700); err != nil {
		runtime.LogErrorf(ctx, "data folder error: %v", err)
	}
	sdb, err := storage.NewDatabase(a.dbPath)
	if err != nil {
		runtime.LogErrorf(ctx, "DB open error: %v", err)
		return
	}
	if err := sdb.ApplyMigrations(); err != nil {
		runtime.LogErrorf(ctx, "DB migration error: %v", err)
	}
	db := sdb.DB
	a.db = db

	// Automatic, idempotent check-and-migrate: encrypt any connections rows
	// left over from before the secrets vault shipped. Cheap (a single COUNT
	// query) once everything is already encrypted, and self-healing if a
	// plaintext row is ever reintroduced.
	if _, _, err := connections.MigrateConnectionsToVault(ctx, db); err != nil {
		runtime.LogErrorf(ctx, "connections migration error: %v", err)
	}
	if _, _, err := secrets.MigrateFieldsToKV(ctx, db); err != nil {
		runtime.LogErrorf(ctx, "vault key-value migration error: %v", err)
	}
	if _, _, err := secrets.MigrateSessionsToVault(ctx, db); err != nil {
		runtime.LogErrorf(ctx, "sessions migration error: %v", err)
	}
	if _, _, err := ai.MigrateProvidersToVault(ctx, db); err != nil {
		runtime.LogErrorf(ctx, "ai providers migration error: %v", err)
	}

	// os.UserHomeDir (not $HOME) so vault/workflow dirs resolve on Windows too.
	home, _ := os.UserHomeDir()

	// Ensure vault directory exists.
	vaultDir := filepath.Join(home, ".monoagent", "vault")
	if err := os.MkdirAll(vaultDir, 0700); err != nil {
		runtime.LogErrorf(ctx, "vault dir error: %v", err)
	}

	// Initialize workflow hybrid store (file + SQLite) so workflows created
	// by both the GUI and the CLI are visible.
	wfDir := filepath.Join(home, ".monoagent", "workflows")
	fileStore, wfErr := workflow.NewWorkflowFileStore(wfDir)
	if wfErr != nil {
		fmt.Printf("workflow file store init error: %v\n", wfErr)
	} else {
		sqlStore := workflow.NewSQLiteWorkflowStore(db)
		a.wfStore = workflow.NewHybridWorkflowStore(fileStore, sqlStore)
	}

	// Initialize connections manager.
	mgr, err := connections.NewManager(a.db)
	if err != nil {
		fmt.Printf("connections manager init error: %v\n", err)
	} else {
		a.connMgr = mgr
	}

	// Initialize AI store.
	aiStore, aiErr := ai.NewAIStore(db)
	if aiErr != nil {
		fmt.Printf("ai store init error: %v\n", aiErr)
	} else {
		a.aiStore = aiStore
		cs := aichat.NewChatService(aiStore, db)
		// Feed the node type registry into canvas tools so AI knows what nodes are available.
		ntMap := a.GetWorkflowNodeTypes()
		var allTypes []aichat.NodeTypeInfo
		for _, v := range ntMap {
			// v is interface{} wrapping a typed slice; marshal+unmarshal to extract
			b, err := json.Marshal(v)
			if err != nil {
				continue
			}
			var items []aichat.NodeTypeInfo
			if err := json.Unmarshal(b, &items); err != nil {
				continue
			}
			allTypes = append(allTypes, items...)
		}
		cs.SetCanvasNodeTypes(allTypes)
		a.chatService = cs
		a.initChatSupervisor(db)
	}

	// Load the active profile from settings; default to 'default' if not set.
	var activeProfileID string
	_ = db.QueryRow(`SELECT value FROM settings WHERE key = 'active_profile_id'`).Scan(&activeProfileID)
	if activeProfileID == "" {
		activeProfileID = "default"
	}
	a.setActiveProfileID(activeProfileID)

	a.migrateProfilesToPerProfileLayout(ctx, db)

	a.restartOrgWatcher()
	a.restartDocumentWatcher()

	a.emitLog("SYSTEM", "INFO", "Mono Agent UI connected to "+a.dbPath)

	// Marks synchronous startup as finished — see IsReady. Must be the last
	// line: everything above (profile resolution in particular) needs to
	// have already happened before any page can trust IsReady()==true.
	a.ready.Store(true)

	go a.backgroundUpdateCheck()
}

// IsReady reports whether startup() has finished its synchronous setup
// (DB migrations, active-profile resolution, node registry, etc.). Wails
// gives no guarantee the frontend won't become interactive before this
// finishes — a page that queries profile-scoped data (e.g. Orgs) landing
// before it does silently reads the wrong ("default") profile until the
// user navigates away and back. Polled by the frontend instead of guessing
// with a fixed delay.
func (a *App) IsReady() bool {
	return a.ready.Load()
}

// migrateProfilesToPerProfileLayout brings every existing profile up to the
// per-profile architecture: a dedicated folder, its files moved out of the
// old shared vault directory and then out of that per-profile folder's own
// legacy vault/ into .monoagent/vault/ (so everything monoagent owns is a
// single unit, distinct from .monomind/ and the user-visible taxonomy
// folders), its secrets re-encrypted under its own key (instead of the one
// every profile used to share), and an empty monomind project bootstrapped
// for its knowledge graph. Runs once per app startup, for every profile —
// each underlying step is already cheap and idempotent once a profile is
// fully migrated (a handful of COUNT-first queries or a no-op MoveFiles),
// the same "run it every startup, no-op once done" pattern the migrations
// right above this call already use (MigrateConnectionsToVault et al.). A
// failure on one profile is logged and does not block the others or app
// startup.
func (a *App) migrateProfilesToPerProfileLayout(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM profiles`)
	if err != nil {
		a.emitLog("SYSTEM", "WARN", fmt.Sprintf("per-profile layout migration: listing profiles: %v", err))
		return
	}
	var profileIDs []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			profileIDs = append(profileIDs, id)
		}
	}
	rows.Close()

	for _, profileID := range profileIDs {
		if err := profiledir.EnsureLayout(db, profileID); err != nil {
			a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: creating folder layout: %v", profileID, err))
			continue
		}

		if moved, errs := vault.MigrateVaultFiles(ctx, db, profileID); moved > 0 || len(errs) > 0 {
			for _, e := range errs {
				a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: vault file migration: %v", profileID, e))
			}
			if moved > 0 {
				a.emitLog("SYSTEM", "INFO", fmt.Sprintf("profile %s: moved %d vault file(s) into its own folder", profileID, moved))
			}
		}

		if moved, errs := vault.MigrateVaultDirIntoMonoagent(ctx, db, profileID); moved > 0 || len(errs) > 0 {
			for _, e := range errs {
				a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: .monoagent vault migration: %v", profileID, e))
			}
			if moved > 0 {
				a.emitLog("SYSTEM", "INFO", fmt.Sprintf("profile %s: moved %d vault file(s) into its own .monoagent folder", profileID, moved))
			}
		}

		if migrated, errs := secrets.MigrateProfileVaultKeys(ctx, db, profileID); migrated > 0 || len(errs) > 0 {
			for _, e := range errs {
				a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: vault key migration: %v", profileID, e))
			}
			if migrated > 0 {
				a.emitLog("SYSTEM", "INFO", fmt.Sprintf("profile %s: re-encrypted %d secret(s) under its own key", profileID, migrated))
			}
		}

		// connections.data and platform_oauth_credentials.client_secret use
		// the same DEK/KEK scheme as vault_secrets but live in a different
		// table, so they need their own migration pass — see
		// connections.MigrateProfileBlobs for why skipping this broke every
		// existing OAuth connection (decrypt failures read as "connection
		// not found," which sends callers into a fresh login flow instead).
		if migrated, errs := connections.MigrateProfileBlobs(ctx, db, profileID); migrated > 0 || len(errs) > 0 {
			for _, e := range errs {
				a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: connection data migration: %v", profileID, e))
			}
			if migrated > 0 {
				a.emitLog("SYSTEM", "INFO", fmt.Sprintf("profile %s: re-encrypted %d connection(s) under its own key", profileID, migrated))
			}
		}

		// Best-effort, fire-and-forget — bootstrapProfileMonograph already
		// handles a not-yet-existing monomind binary or a slow first build
		// without blocking startup.
		a.bootstrapProfileMonograph(profileID)
	}
}

func (a *App) shutdown(_ context.Context) {
	if a.chatSup != nil {
		a.chatSup.stopAll()
	}

	a.orgWatchMu.Lock()
	if a.orgWatcher != nil {
		a.orgWatcher.Stop()
		a.orgWatcher = nil
	}
	a.orgWatchMu.Unlock()

	a.docWatchMu.Lock()
	if a.docWatcher != nil {
		a.docWatcher.Stop()
		a.docWatcher = nil
	}
	if a.capWatcher != nil {
		a.capWatcher.Stop()
		a.capWatcher = nil
	}
	a.docWatchMu.Unlock()

	a.runningMu.Lock()
	for _, cmd := range a.runningCmds {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	a.runningMu.Unlock()
	if a.db != nil {
		_ = a.db.Close()
	}
}

// orgsDirForActiveProfile resolves the directory the org design watcher
// should poll for the currently active profile. Mirrors orgProjectRoot's
// (app_orgs.go) own fallback exactly: when the active profile has no
// resolvable root, `monoagentcli org` (called without --project) resolves
// the active profile's folder itself, which for an unset profile is the
// "default" profile's folder — the watcher must watch that same directory
// or it silently watches nothing for a profile whose layout failed to
// initialize.
func (a *App) orgsDirForActiveProfile() string {
	root := a.orgProjectRoot()
	if root == "" {
		root = profiledir.Root(a.db, "default")
	}
	if root == "" {
		return ""
	}
	return orgdesign.OrgsDir(root)
}

// restartOrgWatcher stops any existing org design watcher and starts a new
// one scoped to the active profile's orgs directory — the mechanism by
// which AI-driven and externally-made org edits (both happen in a separate
// OS process from this app; see internal/ai/chat/monoagent_tools.go) reach
// the live "org:designUpdated" event, since no direct runtime.EventsEmit
// call is possible from outside this process.
func (a *App) restartOrgWatcher() {
	a.orgWatchMu.Lock()
	defer a.orgWatchMu.Unlock()

	if a.orgWatcher != nil {
		a.orgWatcher.Stop()
		a.orgWatcher = nil
	}

	dir := a.orgsDirForActiveProfile()
	if dir == "" {
		return
	}
	w := orgdesign.NewWatcher(dir, 0, func(c orgdesign.Change) {
		a.emitOrgDesignUpdated(c.Name, "external", c.Deleted, c.Doc, len(c.Errors) == 0, c.Errors)
	})
	w.Start()
	a.orgWatcher = w
}

// newUUID generates a random UUID v4 without external dependencies.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (a *App) emitLog(source, level, message string) {
	entry := LogEntry{
		Time:    time.Now().Format("15:04:05"),
		Source:  source,
		Level:   level,
		Message: message,
	}
	a.logsMu.Lock()
	a.logs = append(a.logs, entry)
	if len(a.logs) > 500 {
		a.logs = a.logs[len(a.logs)-500:]
	}
	a.logsMu.Unlock()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "log:entry", entry)
	}
}

// OpenURL opens a URL in the system default browser.
func (a *App) OpenURL(url string) {
	runtime.BrowserOpenURL(a.ctx, url)
}

// Tags
// ─────────────────────────────────────────────────────────────────────────────

type TagInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// GetAllTags returns every tag in the active profile, ordered by name.
func (a *App) GetAllTags() []TagInfo {
	if a.db == nil {
		return nil
	}
	rows, err := a.db.Query(`SELECT id, name, color FROM tags WHERE profile_id = ? ORDER BY name COLLATE NOCASE`, a.getActiveProfileID())
	if err != nil {
		return nil
	}
	defer rows.Close()
	var tags []TagInfo
	for rows.Next() {
		var t TagInfo
		if rows.Scan(&t.ID, &t.Name, &t.Color) == nil {
			tags = append(tags, t)
		}
	}
	return tags
}

// GetPersonTags returns all tags attached to the given person.
func (a *App) GetPersonTags(personId string) []TagInfo {
	if a.db == nil {
		return nil
	}
	rows, err := a.db.Query(`
		SELECT t.id, t.name, t.color
		FROM tags t
		JOIN people_tags pt ON pt.tag_id = t.id
		JOIN people p ON pt.person_id = p.id
		WHERE pt.person_id = ? AND t.profile_id = ? AND p.profile_id = ?
		ORDER BY t.name COLLATE NOCASE`, personId, a.getActiveProfileID(), a.getActiveProfileID())
	if err != nil {
		return nil
	}
	defer rows.Close()
	var tags []TagInfo
	for rows.Next() {
		var t TagInfo
		if rows.Scan(&t.ID, &t.Name, &t.Color) == nil {
			tags = append(tags, t)
		}
	}
	return tags
}

// AddPersonTag tags a person, creating the tag when new; a colour recolours
// an existing tag everywhere — `people tag add`. Returns the tag, or nil
// when it could not be added (unknown person, bad colour, 10 tags already).
func (a *App) AddPersonTag(personId, tagName, color string) *TagInfo {
	args := []string{"people", "tag", "add", personId, tagName}
	if color != "" {
		args = append(args, "--color", color)
	}
	var t TagInfo
	if err := a.runMonoCLI("", &t, args...); err != nil {
		a.emitLog("PEOPLE", "WARN", fmt.Sprintf("tagging %s with %q: %v", personId, tagName, err))
		return nil
	}
	return &t
}

// UpdateTagColor recolours a tag for everyone who has it — `people tag color`.
func (a *App) UpdateTagColor(tagId, color string) bool {
	if err := a.runMonoCLI("", nil, "people", "tag", "color", tagId, color); err != nil {
		a.emitLog("PEOPLE", "WARN", fmt.Sprintf("recolouring tag %s: %v", tagId, err))
		return false
	}
	return true
}

// RemovePersonTag unlinks a tag from a person (the tag itself stays) —
// `people tag remove`.
func (a *App) RemovePersonTag(personId, tagId string) {
	if err := a.runMonoCLI("", nil, "people", "tag", "remove", personId, tagId); err != nil {
		a.emitLog("PEOPLE", "WARN", fmt.Sprintf("untagging %s: %v", personId, err))
	}
}

// GetPeopleTagsMap returns a map of personId → []TagInfo for a slice of person IDs.
// Used to bulk-load tags for the People list without N queries.
func (a *App) GetPeopleTagsMap(personIds []string) map[string][]TagInfo {
	if a.db == nil || len(personIds) == 0 {
		return nil
	}

	// Build IN clause.
	placeholders := make([]string, len(personIds))
	args := make([]interface{}, len(personIds))
	for i, id := range personIds {
		placeholders[i] = "?"
		args[i] = id
	}
	args = append(args, a.getActiveProfileID(), a.getActiveProfileID())
	query := fmt.Sprintf(`
		SELECT pt.person_id, t.id, t.name, t.color
		FROM people_tags pt
		JOIN tags t ON t.id = pt.tag_id
		JOIN people p ON pt.person_id = p.id
		WHERE pt.person_id IN (%s) AND t.profile_id = ? AND p.profile_id = ?
		ORDER BY t.name COLLATE NOCASE`, strings.Join(placeholders, ","))

	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	result := make(map[string][]TagInfo)
	for rows.Next() {
		var pid string
		var t TagInfo
		if rows.Scan(&pid, &t.ID, &t.Name, &t.Color) == nil {
			result[pid] = append(result[pid], t)
		}
	}
	return result
}

// ─────────────────────────────────────────────────────────────────────────────
// Social Lists
// ─────────────────────────────────────────────────────────────────────────────

type SocialListInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ListType  string `json:"list_type"`
	ItemCount int    `json:"item_count"`
	CreatedAt string `json:"created_at"`
}

func (a *App) GetSocialLists() []SocialListInfo {
	if a.db == nil {
		return nil
	}
	rows, err := a.db.Query(`SELECT id, name, COALESCE(list_type,''), item_count, COALESCE(created_at,'')
	                          FROM social_lists WHERE profile_id = ? ORDER BY created_at DESC`, a.getActiveProfileID())
	if err != nil {
		return nil
	}
	defer rows.Close()
	var lists []SocialListInfo
	for rows.Next() {
		var l SocialListInfo
		if rows.Scan(&l.ID, &l.Name, &l.ListType, &l.ItemCount, &l.CreatedAt) == nil {
			lists = append(lists, l)
		}
	}
	return lists
}

// ─────────────────────────────────────────────────────────────────────────────
// Templates
// ─────────────────────────────────────────────────────────────────────────────

type TemplateInfo struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (a *App) GetTemplates() []TemplateInfo {
	if a.db == nil {
		return nil
	}
	rows, err := a.db.Query("SELECT id, name, COALESCE(subject,''), body FROM templates ORDER BY name")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var templates []TemplateInfo
	for rows.Next() {
		var t TemplateInfo
		if rows.Scan(&t.ID, &t.Name, &t.Subject, &t.Body) == nil {
			templates = append(templates, t)
		}
	}
	return templates
}

// ─────────────────────────────────────────────────────────────────────────────
// Action Execution
// ─────────────────────────────────────────────────────────────────────────────

// findMonoAgentCLI locates the monoagentcli binary.
func findMonoAgentCLI() (string, error) {
	// Explicit override — checked first so a stale PATH-installed
	// monoagentcli (e.g. an old system-wide `go install`/release binary
	// that predates a given dev build's new commands) can't silently shadow
	// the binary a developer actually wants `wails dev` to shell out to.
	if p := strings.TrimSpace(os.Getenv("MONOAGENTCLI_BIN")); p != "" {
		if fileExists(p) {
			return p, nil
		}
	}
	// Also check relative to executable (bundled app or dev bin/ directory).
	// Checked before PATH so a sibling binary built with the app is preferred over
	// an older version installed globally in ~/.local/bin or /usr/local/bin.
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		if p, ok := siblingCLI(filepath.Dir(exe), goruntime.GOOS, goruntime.GOARCH); ok {
			return p, nil
		}
	}

	if p, err := exec.LookPath("monoagentcli"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	name := cliSiblingNames(goruntime.GOOS, goruntime.GOARCH)[0]
	candidates := []string{
		filepath.Join(home, "go", "bin", name),
		filepath.Join(home, ".local", "bin", name),
		"/usr/local/bin/monoagentcli",
		"/opt/homebrew/bin/monoagentcli",
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		execDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(execDir, "..", "..", "..", "cmd", "monoagentcli", "monoagentcli"),
		)
	}
	for _, p := range candidates {
		if fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("monoagentcli binary not found — run `go install` or place the binary in PATH")
}

// cliSiblingNames are the file names the CLI has next to the desktop app:
// "monoagentcli" (".exe" on Windows; macOS .app bundles and the current
// Linux tarball), then the release's bundled name
// "monoagentcli-<goos>-<goarch>-bundled[.exe]" (older Linux tarballs, and
// the separate Windows download saved beside MonoAgent-windows-amd64.exe).
func cliSiblingNames(goos, goarch string) []string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return []string{
		"monoagentcli" + ext,
		"monoagentcli-" + goos + "-" + goarch + "-bundled" + ext,
	}
}

// siblingCLI returns the first CLI found in execDir under cliSiblingNames.
func siblingCLI(execDir, goos, goarch string) (string, bool) {
	for _, n := range cliSiblingNames(goos, goarch) {
		if p := filepath.Join(execDir, n); fileExists(p) {
			return p, true
		}
	}
	return "", false
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// nodeTypeToPlatform derives the connection platform ID from a node type string.
// e.g. "service.google_sheets" → "google_sheets", "db.postgres" → "postgresql"
func nodeTypeToPlatform(nodeType string) string {
	overrides := map[string]string{
		"db.postgres":     "postgresql",
		"db.mysql":        "mysql",
		"db.mongodb":      "mongodb",
		"db.redis":        "redis",
		"comm.email_send": "smtp",
		"comm.email_read": "imap",
	}
	if p, ok := overrides[nodeType]; ok {
		return p
	}
	parts := strings.SplitN(nodeType, ".", 2)
	if len(parts) == 2 {
		return parts[1] // "service.google_sheets" → "google_sheets"
	}
	return nodeType
}

// ─────────────────────────────────────────────────────────────────────────────
// Data export
// ─────────────────────────────────────────────────────────────────────────────

// ExportResult summarizes a completed export.
type ExportResult struct {
	OutputDir   string `json:"output_dir"`
	PeopleCount int    `json:"people_count"`
	Cancelled   bool   `json:"cancelled,omitempty"`
}

// ExportData asks the user for a destination folder and exports all people
// to JSON files there by invoking the CLI (`monoagentcli export`).
func (a *App) ExportData() (*ExportResult, error) {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return nil, err
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose export folder"})
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return &ExportResult{Cancelled: true}, nil
	}
	cmd := exec.CommandContext(a.ctx, cliBin, "--profile", a.getActiveProfileID(), "--json", "export", "--output-dir", dir)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("export failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("export failed: %w", err)
	}
	var res ExportResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("unexpected export output: %w", err)
	}
	a.emitLog("EXPORT", "INFO", fmt.Sprintf("Exported %d people to %s", res.PeopleCount, res.OutputDir))
	return &res, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Logs
// ─────────────────────────────────────────────────────────────────────────────

type LogEntry struct {
	Time    string `json:"time"`
	Source  string `json:"source"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

func (a *App) GetLogs() []LogEntry {
	a.logsMu.Lock()
	defer a.logsMu.Unlock()
	out := make([]LogEntry, len(a.logs))
	copy(out, a.logs)
	return out
}

func (a *App) ClearLogs() {
	a.logsMu.Lock()
	a.logs = make([]LogEntry, 0, 200)
	a.logsMu.Unlock()
}

// ─────────────────────────────────────────────────────────────────────────────
// Metadata
// ─────────────────────────────────────────────────────────────────────────────

func (a *App) GetDBPath() string {
	return a.dbPath
}

func (a *App) IsDBConnected() bool {
	if a.db == nil {
		return false
	}
	return a.db.Ping() == nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Profiles
// ─────────────────────────────────────────────────────────────────────────────

type ProfileInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsActive  bool   `json:"is_active"`
	CreatedAt string `json:"created_at"`
	// RootDir is the real, resolved folder this profile's data lives in —
	// always populated (even for profiles using the default location), so
	// the frontend never needs to know the fallback rule itself.
	RootDir string `json:"root_dir"`
	// Icon is an id into the shared agent-avatars.json manifest (the same
	// one Org Designer role icons use), or "" if none was chosen — profiles
	// created before this field existed are always "".
	Icon string `json:"icon"`
}

func (a *App) GetProfiles() ([]ProfileInfo, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := a.db.Query(`SELECT id, name, created_at, icon FROM profiles ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []ProfileInfo
	for rows.Next() {
		var p ProfileInfo
		if rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.Icon) == nil {
			p.IsActive = p.ID == a.getActiveProfileID()
			p.RootDir = profiledir.Root(a.db, p.ID)
			profiles = append(profiles, p)
		}
	}
	if profiles == nil {
		profiles = []ProfileInfo{}
	}
	return profiles, rows.Err()
}

// CreateProfile creates a new profile. rootDir, if non-empty, is the folder
// the user picked (via ChooseProfileFolder, or a suggested monomind project
// via ListMonomindProjects) for this profile's data instead of the default
// ~/.monoagent/profiles/<id>/ — it must be an absolute path; see
// validateFolderChoice for what else is required of it (an existing folder
// need not be empty — e.g. an existing coding project you already run
// monomind/Claude Code in is a perfectly good choice — but it must be safe
// to layer this profile's .monoagent/ and .monomind/ subfolders onto). icon,
// if non-empty, is an id into the shared agent-avatars.json manifest;
// leaving it empty is fine, the frontend falls back to a placeholder.
func (a *App) CreateProfile(name, rootDir, icon string) (*ProfileInfo, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("profile name cannot be empty")
	}
	rootDir = strings.TrimSpace(rootDir)
	if rootDir != "" {
		if err := validateFolderChoice(rootDir); err != nil {
			return nil, err
		}
	}
	icon = strings.TrimSpace(icon)
	id := newUUID()
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	_, err := a.db.Exec(`INSERT INTO profiles (id, name, created_at, root_dir, icon) VALUES (?, ?, ?, ?, ?)`, id, name, now, rootDir, icon)
	if err != nil {
		return nil, fmt.Errorf("create profile: %w", err)
	}
	if err := profiledir.EnsureLayout(a.db, id); err != nil {
		// Non-fatal: the profile row exists and is usable; its dedicated
		// folder (vault files, knowledge graph) just won't be there until
		// the next startup migration pass retries it.
		a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: creating folder layout: %v", id, err))
	} else {
		a.bootstrapProfileMonograph(id)
	}
	return &ProfileInfo{ID: id, Name: name, IsActive: false, CreatedAt: now, RootDir: profiledir.Root(a.db, id), Icon: icon}, nil
}

// validateFolderChoice rejects a folder choice that isn't safe to hand a
// profile's data to. The folder need not be empty or new — pointing a
// profile at an existing, non-empty folder (e.g. a coding project that
// already has its own .monomind/ from a prior `monomind init`) is fine:
// EnsureLayout only ever adds a .monoagent/ and a .monomind/ subfolder
// inside it and never touches anything else there, so nothing pre-existing
// is at risk except those two specific names. What's actually unsafe, and
// rejected here, is a `.monoagent` or `.monomind` entry that already exists
// as a plain file rather than a directory — os.MkdirAll would fail
// confusingly on that, so it's caught up front with a clear message instead.
func validateFolderChoice(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("folder path must be absolute: %q", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // doesn't exist yet — fine, EnsureLayout creates it
		}
		return fmt.Errorf("checking folder %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a folder", dir)
	}
	for _, name := range []string{".monoagent", ".monomind"} {
		entryInfo, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue // doesn't exist — fine, EnsureLayout creates it
		}
		if !entryInfo.IsDir() {
			return fmt.Errorf("%q already contains a file named %q — move or rename it first", dir, name)
		}
	}
	return nil
}

// bootstrapProfileMonograph seeds an empty monograph database at a fresh
// profile's .monomind/ project root, so it exists (even if empty) before the
// first chat turn or knowledge-graph write ever asks for it. Best-effort and
// fire-and-forget — a failure here never blocks profile creation, since
// monograph_build/kgIngest will lazily create what they need on first real
// use regardless.
func (a *App) bootstrapProfileMonograph(profileID string) {
	db := a.db
	go func() {
		bin, err := monomind.Find()
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "monograph", "build", "--path", profiledir.MonomindDir(db, profileID))
		hideWindow(cmd)
		if err := cmd.Run(); err != nil {
			a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: monograph bootstrap: %v", profileID, err))
		}
	}()
}

// ChooseProfileFolder opens a native folder picker and returns the chosen
// absolute path, or "" if the user cancelled — same shape as ExportData's
// existing use of the same dialog.
func (a *App) ChooseProfileFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose a folder for this profile",
	})
}

// RevealProfileFolder opens a Finder window at a profile's current folder —
// distinct from ChooseProfileFolder/MoveProfileFolder, which change where
// the folder is; this just shows where it already is.
func (a *App) RevealProfileFolder(profileID string) error {
	if a.db == nil {
		return fmt.Errorf("database not available")
	}
	dir := profiledir.Root(a.db, profileID)
	if err := profiledir.EnsureLayout(a.db, profileID); err != nil {
		return fmt.Errorf("preparing profile folder: %w", err)
	}
	return revealFolder(dir)
}

// revealFolder shells out to the platform's file-manager "reveal" command
// for dir. Split out from RevealProfileFolder so tests can exercise the
// per-GOOS command selection without spawning a real GUI file manager.
func revealFolder(dir string) error {
	name, args := revealFolderCommand(goruntime.GOOS, dir)
	cmd := exec.Command(name, args...)
	hideWindow(cmd)
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if goruntime.GOOS == "windows" {
		// explorer.exe is documented to return a non-zero exit status even
		// when it successfully opened the folder (a known quirk unrelated
		// to whether the folder was revealed). Only swallow that case —
		// the process ran but reported a non-zero exit — not a failure to
		// launch it at all (e.g. explorer missing from PATH), which should
		// still surface to the caller.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
	}
	return err
}

// revealFolderCommand returns the OS-appropriate command name and arguments
// to reveal dir in the platform's file manager: Finder ("open") on darwin,
// "explorer" on windows, and "xdg-open" everywhere else (linux and other
// unix-likes) — the three platforms the release pipeline ships CLI/GUI
// builds for.
func revealFolderCommand(goos, dir string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{dir}
	case "windows":
		return "explorer", []string{dir}
	default:
		return "xdg-open", []string{dir}
	}
}

// MoveProfileFolder moves an existing profile's data (vault files and its
// .monomind knowledge-graph directory) from its current folder to
// newRootDir, then — only once both moves have actually succeeded — updates
// profiles.root_dir to point at the new location. On any failure partway
// through, root_dir is left unchanged so the database never points
// somewhere the files didn't actually make it to; the old location stays
// authoritative and the caller can retry.
//
// Orgs run out of the folder, so before anything moves the folder's running
// orgs and its `org serve` daemon are stopped (the move is refused if that
// fails), and afterwards the org files are reconciled and the daemon is
// restarted wherever the folder ended up (C-24, app_org_profile.go).
func (a *App) MoveProfileFolder(profileID, newRootDir string) error {
	if a.db == nil {
		return fmt.Errorf("database not available")
	}
	newRootDir = strings.TrimSpace(newRootDir)
	if newRootDir == "" {
		return fmt.Errorf("no folder chosen")
	}
	if err := validateFolderChoice(newRootDir); err != nil {
		return err
	}

	oldRoot := profiledir.Root(a.db, profileID)
	if newRootDir == oldRoot {
		return fmt.Errorf("that's already this profile's folder")
	}

	wasServing, err := a.stopProfileOrgs(profileID)
	if err != nil {
		return fmt.Errorf("stopping this profile's orgs before the move: %w", err)
	}
	// Whether or not the move lands, the orgs resume at whatever folder
	// profiles.root_dir names once this returns.
	defer a.resumeProfileOrgs(profileID, wasServing)

	oldVaultDir := profiledir.VaultDir(a.db, profileID)
	oldMonomindDir := profiledir.MonomindDir(a.db, profileID)
	newVaultDir := filepath.Join(newRootDir, ".monoagent", "vault")
	newMonomindDir := filepath.Join(newRootDir, ".monomind")

	if err := os.MkdirAll(newVaultDir, 0700); err != nil {
		return fmt.Errorf("creating new vault folder: %w", err)
	}
	if err := os.MkdirAll(newMonomindDir, 0700); err != nil {
		return fmt.Errorf("creating new monomind folder: %w", err)
	}

	if _, errs := vault.MoveFiles(a.ctx, a.db, profileID, oldVaultDir, newVaultDir); len(errs) > 0 {
		for _, e := range errs {
			a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: moving vault file: %v", profileID, e))
		}
		return fmt.Errorf("moving vault files: %v (see Live Logs for per-file detail)", errs[0])
	}

	// .monomind holds only monograph/KG SQLite files with no cross-references
	// elsewhere in the database (unlike vault_images.path), so moving the
	// directory itself is sufficient. os.Rename fails across filesystems
	// (e.g. moving onto an external drive) — fall back to copy-then-remove.
	if err := os.Rename(oldMonomindDir, newMonomindDir); err != nil {
		if err := copyDirThenRemove(oldMonomindDir, newMonomindDir); err != nil {
			return fmt.Errorf("moving monomind folder: %w", err)
		}
	}

	if _, err := a.db.Exec(`UPDATE profiles SET root_dir = ? WHERE id = ?`, newRootDir, profileID); err != nil {
		return fmt.Errorf("updating profile folder: %w", err)
	}
	if profileID == a.getActiveProfileID() {
		a.restartOrgWatcher()
		a.restartDocumentWatcher()
	}
	a.emitLog("SYSTEM", "INFO", fmt.Sprintf("profile %s: moved to %s", profileID, newRootDir))
	return nil
}

// copyDirThenRemove copies src's tree into dst (which must already exist),
// then removes src — MoveProfileFolder's fallback for a .monomind move that
// crosses filesystems, where os.Rename fails.
func copyDirThenRemove(src, dst string) error {
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(destPath, 0700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func (a *App) SwitchProfile(id string) error {
	if a.db == nil {
		return fmt.Errorf("database not available")
	}
	// Verify the profile exists.
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM profiles WHERE id = ?`, id).Scan(&count); err != nil || count == 0 {
		return fmt.Errorf("profile %q not found", id)
	}
	// Persist selection — does NOT kill any running workflow subprocesses.
	_, err := a.db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('active_profile_id', ?)`, id)
	if err != nil {
		return fmt.Errorf("persist active profile: %w", err)
	}
	a.setActiveProfileID(id)
	a.restartOrgWatcher()
	a.restartDocumentWatcher()
	a.emitLog("SYSTEM", "INFO", "Switched to profile: "+id)
	return nil
}

func (a *App) GetActiveProfile() (*ProfileInfo, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	var p ProfileInfo
	err := a.db.QueryRow(`SELECT id, name, created_at FROM profiles WHERE id = ?`, a.getActiveProfileID()).
		Scan(&p.ID, &p.Name, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("active profile not found: %w", err)
	}
	p.IsActive = true
	return &p, nil
}

// Human in Loop (HIL)
// ─────────────────────────────────────────────────────────────────────────────

// HILItem is the data structure returned to the frontend for each pending HIL item.
type HILItem struct {
	ID           string                 `json:"id"`
	ExecutionID  string                 `json:"execution_id"`
	WorkflowID   string                 `json:"workflow_id"`
	WorkflowName string                 `json:"workflow_name"`
	NodeID       string                 `json:"node_id"`
	NodeName     string                 `json:"node_name"`
	Status       string                 `json:"status"`
	ReadonlyData map[string]interface{} `json:"readonly_data"`
	EditableData map[string]interface{} `json:"editable_data"`
	NodeConfig   map[string]interface{} `json:"node_config"`
	CreatedAt    string                 `json:"created_at"`
}

// GetHILItems returns the active profile's pending Human-in-Loop items,
// including the workflow name — `hil list --suggest`. A TypeSafe Jev
// suggestion (when the profile enabled surface hil) is in
// node_config.suggestion; the CLI stores it, so polling never re-pays.
func (a *App) GetHILItems() ([]HILItem, error) {
	items := []HILItem{}
	if err := a.runMonoCLI("", &items, "hil", "list", "--suggest"); err != nil {
		return nil, fmt.Errorf("GetHILItems: %w", err)
	}
	if items == nil {
		items = []HILItem{}
	}
	return items, nil
}

// ApproveHIL approves a pending HIL item with optional edited data (JSON
// string) — `hil approve <id> --data …`.
func (a *App) ApproveHIL(id string, editedDataJSON string) error {
	if err := a.resolveHIL(id, editedDataJSON, true); err != nil {
		return err
	}
	a.emitLog("HIL", "INFO", fmt.Sprintf("HIL item %s approved", id))
	return nil
}

// RejectHIL rejects a pending HIL item, causing the workflow to error out —
// `hil reject <id>`.
func (a *App) RejectHIL(id string) error {
	if err := a.resolveHIL(id, "", false); err != nil {
		return err
	}
	a.emitLog("HIL", "INFO", fmt.Sprintf("HIL item %s rejected", id))
	return nil
}

// resolveHIL runs `hil approve|reject` for the active profile.
func (a *App) resolveHIL(id, editedDataJSON string, approve bool) error {
	if !approve {
		if err := a.runMonoCLI("", nil, "hil", "reject", id); err != nil {
			return fmt.Errorf("RejectHIL: %w", err)
		}
		return nil
	}
	if editedDataJSON == "" {
		editedDataJSON = "{}"
	}
	var check map[string]interface{}
	if err := json.Unmarshal([]byte(editedDataJSON), &check); err != nil {
		return fmt.Errorf("ApproveHIL: editedDataJSON is not valid JSON: %w", err)
	}
	if err := a.runMonoCLI("", nil, "hil", "approve", id, "--data", editedDataJSON); err != nil {
		return fmt.Errorf("ApproveHIL: %w", err)
	}
	return nil
}
