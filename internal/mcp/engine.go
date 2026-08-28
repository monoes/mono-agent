package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// runtime holds the lazily-bootstrapped database, profile, store, and
// (on first workflow_run) a running WorkflowEngine. It mirrors the builder
// logic in cmd/monoagentcli (initDB + buildEngine) without importing cmd.
type runtime struct {
	db        *storage.Database
	profileID string
	store     *workflow.HybridWorkflowStore
	registry  *workflow.NodeTypeRegistry
	engine    *workflow.WorkflowEngine
	sched     *scheduler.Scheduler
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

// runtime lazily bootstraps and caches the shared runtime for a server.
func (s *Server) runtime() (*runtime, error) {
	if s.rt != nil {
		return s.rt, nil
	}
	rt, err := newRuntime(s.opts)
	if err != nil {
		return nil, err
	}
	s.rt = rt
	return rt, nil
}

// Close releases the runtime's engine and database. Safe on nil receivers
// and safe to call more than once.
func (rt *runtime) Close() {
	if rt == nil {
		return
	}
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

// newRuntime opens the database (applying migrations), resolves the active
// profile, and builds the hybrid workflow store.
func newRuntime(opts Options) (*runtime, error) {
	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = "~/.monoagent/monoagent.db"
	}
	dbPath = expandHome(dbPath)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, fmt.Errorf("mcp: create db dir: %w", err)
	}
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("mcp: open database: %w", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("mcp: apply migrations: %w", err)
	}

	// Best-effort vault migrations, mirroring the CLI's initDB.
	ctx := context.Background()
	if _, _, err := connections.MigrateConnectionsToVault(ctx, db.DB); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: warning: connections migration: %v\n", err)
	}
	if _, _, err := secrets.MigrateFieldsToKV(ctx, db.DB); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: warning: vault key-value migration: %v\n", err)
	}
	if _, _, err := secrets.MigrateSessionsToVault(ctx, db.DB); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: warning: sessions migration: %v\n", err)
	}
	if _, _, err := ai.MigrateProvidersToVault(ctx, db.DB); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: warning: ai providers migration: %v\n", err)
	}

	profile := opts.Profile
	if profile == "" {
		profile = os.Getenv("MONOAGENT_PROFILE")
	}
	if profile != "" {
		resolved, err := resolveProfileID(db.DB, profile)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("mcp: %w", err)
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

// ensureEngine starts the WorkflowEngine (queue workers, scheduler, resume
// loop) on first use. Browser-session nodes are not wired here: an MCP
// server never launches Chrome; run those workflows via the CLI or daemon.
func (rt *runtime) ensureEngine(ctx context.Context) error {
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
		return fmt.Errorf("mcp: start engine: %w", err)
	}

	rt.registry = registry
	rt.sched = sched
	rt.engine = engine
	return nil
}

// registryOrDefault returns the node registry, building a DB-less one if
// the engine has not been started yet.
func (rt *runtime) registryOrDefault() *workflow.NodeTypeRegistry {
	if rt.registry != nil {
		return rt.registry
	}
	return noderegistry.Build(nil)
}
