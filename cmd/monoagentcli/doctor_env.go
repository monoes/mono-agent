package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/monoes/mono-agent/internal/health"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/nodemgr"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/monoes/mono-agent/internal/shellpath"
	"github.com/monoes/mono-agent/internal/storage"
)

// newHealthEnv builds the real-machine environment for checks and fixes.
// The database is opened only if it already exists and is never migrated
// here — checks must not change anything; the migrate fix does that.
func newHealthEnv(cfg *globalConfig) (*health.Env, func()) {
	home, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	dbPath := expandPath(cfg.DBPath)
	env := &health.Env{
		Home:       home,
		DataDir:    filepath.Join(home, ".monoagent"),
		DBPath:     dbPath,
		Version:    getVersion(),
		Executable: exe,
		ProfileID:  cfg.ProfileID,
		LoginPath:  shellpath.LoginPath,
		FreeBytes:  health.FreeBytes,
		LatestVersion: func(ctx context.Context) (string, error) {
			rel, err := fetchLatestRelease(ctx)
			if err != nil {
				return "", err
			}
			return rel.TagName, nil
		},
		Migrate: func(_ context.Context, progress func(string)) error {
			// The migration runner reports through the standard logger;
			// route it into the fix's progress stream for the duration.
			restore := captureStdLog(progress)
			defer restore()
			db, err := initDB(&globalConfig{DBPath: cfg.DBPath, ProfileID: cfg.ProfileID})
			if err != nil {
				return err
			}
			return db.Close()
		},
	}
	nm := nodemgr.New()
	env.SystemNode = nm.SystemNode
	env.ManagedNode = func() (string, string, bool) {
		v, ok := nm.Current()
		if !ok {
			return "", "", false
		}
		return v, nm.NodePath(v), true
	}
	env.InstallNode = func(ctx context.Context, progress func(string)) error {
		if _, err := nm.Install(ctx, "lts", progress); err != nil {
			return err
		}
		// Later fixes in this same run (installing monomind with it) need
		// it on PATH now, not at the next start.
		nodemgr.Activate(ctx)
		return nil
	}
	var hsOnce sync.Once
	var hsInfo *monomind.VersionInfo
	var hsErr error
	env.FindMonomind = monomind.Find
	env.MonomindHandshake = func(ctx context.Context) (*monomind.VersionInfo, error) {
		hsOnce.Do(func() { _, hsInfo, hsErr = monomind.Ensure(ctx) })
		return hsInfo, hsErr
	}
	env.ScanRuntimes = monomind.Scan
	env.InstallMonomind = func(ctx context.Context, progress func(string)) error {
		if _, err := nm.InstallGlobal(ctx, progress, health.MonomindPackage); err != nil {
			return err
		}
		monomind.ResetCapabilityCache()
		bin, vi, err := monomind.Ensure(ctx)
		if err != nil {
			return fmt.Errorf("installed, but monomind still does not answer: %w", err)
		}
		progress(fmt.Sprintf("monomind %s ready at %s", vi.Version, bin))
		return nil
	}
	env.InitMonomindProfile = func(ctx context.Context, root string, progress func(string)) error {
		return monomind.InitProfile(ctx, monomind.InitOptions{Root: root, Progress: progress})
	}
	env.ProfileRoot = func(id string) string { return profiledir.Root(env.DB, id) }
	env.EnsureProfile = func(id string) error { return profiledir.EnsureLayout(env.DB, id) }

	closeFn := func() {}
	if _, err := os.Stat(dbPath); err == nil {
		db, err := storage.NewDatabase(dbPath)
		if err != nil {
			env.DBErr = err
		} else {
			env.DB = db.DB
			env.PendingMigrations = db.PendingMigrations
			env.QuickCheck = db.QuickCheck
			env.VaultState = func(ctx context.Context, id string) (string, error) {
				st, err := secrets.CheckVault(ctx, db.DB, id)
				return string(st), err
			}
			closeFn = func() { db.Close() }
			if env.ProfileID == "" {
				var id string
				_ = db.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, profiledir.ActiveProfileSetting).Scan(&id)
				env.ProfileID = id
			}
		}
	} else if !os.IsNotExist(err) {
		env.DBErr = err
	}
	if env.ProfileID == "" {
		env.ProfileID = "default"
	}
	return env, closeFn
}
