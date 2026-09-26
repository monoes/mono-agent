package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
// Profiles (the bindings live in app_profiles.go)
// ─────────────────────────────────────────────────────────────────────────────

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

// ─────────────────────────────────────────────────────────────────────────────
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
