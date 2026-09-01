// Package httpapi implements a read-first REST/JSON surface over the
// workflow engine and vault, for external agents that cannot speak the
// stdio MCP protocol. It mirrors internal/mcp's conventions: a lazily
// bootstrapped runtime (DB + profile + store, engine started on first
// mutating call), the same output-redaction pipeline
// (workflow.RedactAndTruncateItems), and loopback-only binding by default.
package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/ai"
	cfgpkg "github.com/monoes/mono-agent/internal/config"
	"github.com/monoes/mono-agent/internal/connections"
	"github.com/monoes/mono-agent/internal/noderegistry"
	"github.com/monoes/mono-agent/internal/nodes"
	"github.com/monoes/mono-agent/internal/scheduler"
	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// Options configures the server. It mirrors mcp.Options.
type Options struct {
	// DBPath is the SQLite database path ("~/..." expanded). Empty
	// defaults to ~/.monoagent/monoagent.db.
	DBPath string
	// Profile is the --profile flag value. Empty consults
	// MONOAGENT_PROFILE, then the active_profile_id setting, then
	// "default".
	Profile string
	// WorkflowsDir overrides the JSON workflow file store directory
	// (default ~/.monoagent/workflows; mainly for tests).
	WorkflowsDir string
	// Addr is the listen address. Empty defaults to
	// MONOAGENT_HTTPAPI_ADDR, then "127.0.0.1:9322".
	Addr string
	// AllowMutations enables the mutating endpoints (workflow run/
	// activate/deactivate, hil approve/reject). Empty consults
	// MONOAGENT_HTTPAPI_ALLOW_MUTATIONS=="1".
	AllowMutations bool
	// Version is reported by /health.
	Version string
}

// runtime holds the lazily-bootstrapped database, profile, store, and (on
// first mutating call) a running WorkflowEngine. It is a near-verbatim
// copy of internal/mcp's runtime — kept as its own unexported type here
// rather than shared, since internal/mcp does not export it and the two
// packages must stay independently buildable per the repo's internal/
// package boundaries.
type runtime struct {
	db        *storage.Database
	profileID string
	store     *workflow.HybridWorkflowStore
	registry  *workflow.NodeTypeRegistry
	engine    *workflow.WorkflowEngine
	sched     *scheduler.Scheduler

	// mu guards the lazy engine/registry/scheduler bootstrap: HTTP
	// requests are handled concurrently, so several mutating requests may
	// race to start the engine on first use.
	mu sync.Mutex
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// migrateProfilesToPerProfileKeys mirrors internal/mcp/engine.go's function
// of the same name (itself mirroring the CLI's initDB / the wails app's
// migration): every profile's secrets and connection blobs are re-encrypted
// off the shared legacy key onto the profile's own key, before any vault
// use. Idempotent and cheap once fully migrated; non-fatal per profile.
func migrateProfilesToPerProfileKeys(ctx context.Context, db *sql.DB) {
	var legacy int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'vault_keys_legacy'`).Scan(&legacy); err != nil {
		fmt.Fprintf(os.Stderr, "httpapi: warning: vault key migration: %v\n", err)
		return
	}
	if legacy == 0 {
		return
	}

	rows, err := db.QueryContext(ctx, `SELECT id FROM profiles`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "httpapi: warning: vault key migration: listing profiles: %v\n", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		if migrated, errs := secrets.MigrateProfileVaultKeys(ctx, db, id); migrated > 0 || len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "httpapi: warning: profile %s: vault key migration: %v\n", id, e)
			}
		}
		if migrated, errs := connections.MigrateProfileBlobs(ctx, db, id); migrated > 0 || len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "httpapi: warning: profile %s: connection data migration: %v\n", id, e)
			}
		}
	}
}

// newRuntime opens the database (applying migrations), resolves the active
// profile, and builds the hybrid workflow store. Mirrors internal/mcp's
// newRuntime.
func newRuntime(opts Options) (*runtime, error) {
	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = "~/.monoagent/monoagent.db"
	}
	dbPath = expandHome(dbPath)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, fmt.Errorf("httpapi: create db dir: %w", err)
	}
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("httpapi: open database: %w", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("httpapi: apply migrations: %w", err)
	}

	ctx := context.Background()
	if _, _, err := connections.MigrateConnectionsToVault(ctx, db.DB); err != nil {
		fmt.Fprintf(os.Stderr, "httpapi: warning: connections migration: %v\n", err)
	}
	if _, _, err := secrets.MigrateFieldsToKV(ctx, db.DB); err != nil {
		fmt.Fprintf(os.Stderr, "httpapi: warning: vault key-value migration: %v\n", err)
	}
	if _, _, err := secrets.MigrateSessionsToVault(ctx, db.DB); err != nil {
		fmt.Fprintf(os.Stderr, "httpapi: warning: sessions migration: %v\n", err)
	}
	if _, _, err := ai.MigrateProvidersToVault(ctx, db.DB); err != nil {
		fmt.Fprintf(os.Stderr, "httpapi: warning: ai providers migration: %v\n", err)
	}
	migrateProfilesToPerProfileKeys(ctx, db.DB)

	profile := opts.Profile
	if profile == "" {
		profile = os.Getenv("MONOAGENT_PROFILE")
	}
	if profile != "" {
		resolved, err := resolveProfileID(db.DB, profile)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("httpapi: %w", err)
		}
		profile = resolved
	} else {
		var id string
		_ = db.DB.QueryRow(`SELECT value FROM settings WHERE key = 'active_profile_id'`).Scan(&id)
		if id == "" {
			id = "default"
		}
		profile = id
	}

	sqlStore := workflow.NewSQLiteWorkflowStore(db.DB)
	wfDir := opts.WorkflowsDir
	if wfDir == "" {
		wfDir = "~/.monoagent/workflows"
	}
	fileStore, err := workflow.NewWorkflowFileStore(expandHome(wfDir))
	if err != nil {
		fileStore = nil
	}

	return &runtime{
		db:        db,
		profileID: profile,
		store:     workflow.NewHybridWorkflowStore(fileStore, sqlStore),
	}, nil
}

// resolveProfileID accepts a profile's ID or display name and returns the
// canonical ID (same semantics as the CLI's --profile resolution).
func resolveProfileID(db *sql.DB, idOrName string) (string, error) {
	var id string
	if err := db.QueryRow(`SELECT id FROM profiles WHERE id = ?`, idOrName).Scan(&id); err == nil {
		return id, nil
	}
	if err := db.QueryRow(`SELECT id FROM profiles WHERE name = ?`, idOrName).Scan(&id); err == nil {
		return id, nil
	}
	return "", fmt.Errorf("profile %q not found (checked both id and name)", idOrName)
}

// Close releases the runtime's engine and database. Safe on nil receivers
// and safe to call more than once.
func (rt *runtime) Close() {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.engine != nil {
		_ = rt.engine.Stop()
		rt.engine = nil
	}
	if rt.sched != nil {
		_ = rt.sched.Stop()
		rt.sched = nil
	}
	if rt.db != nil {
		_ = rt.db.Close()
		rt.db = nil
	}
}

// ensureEngine starts the WorkflowEngine (queue workers, scheduler, resume
// loop) on first use. Browser-session nodes are not wired here — like the
// MCP server, the HTTP API never launches Chrome; run those workflows via
// the CLI or daemon.
func (rt *runtime) ensureEngine(ctx context.Context) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.engine != nil {
		return nil
	}
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger().Level(zerolog.WarnLevel)

	registry := noderegistry.Build(rt.db.DB)
	nodes.SetGlobalCredentialStore(connections.NewStore(rt.db.DB))
	cfgStore := cfgpkg.ConfigStore(&cfgpkg.DBConfigStore{DB: rt.db})
	nodes.SetGlobalConfigMgr(&cfgpkg.ConfigManagerAdapter{
		Mgr: cfgpkg.NewConfigManager(expandHome("~/.monoagent/configs"), cfgStore, logger),
	})

	sched := scheduler.NewScheduler(logger)
	sched.Start()

	engine := workflow.NewWorkflowEngineWithStore(rt.store, rt.db.DB, sched, registry, workflow.EngineConfig{
		MaxConcurrent:  5,
		QueueCapacity:  1000,
		PruneInterval:  time.Hour,
		MaxExecHistory: 500,
		ProfileID:      rt.profileID,
	}, logger)
	if err := engine.Start(ctx); err != nil {
		sched.Stop()
		return fmt.Errorf("httpapi: start engine: %w", err)
	}

	rt.registry = registry
	rt.sched = sched
	rt.engine = engine
	return nil
}

// registryOrDefault returns the node registry, building a DB-less one if
// the engine has not been started yet.
func (rt *runtime) registryOrDefault() *workflow.NodeTypeRegistry {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.registry != nil {
		return rt.registry
	}
	return noderegistry.Build(nil)
}
