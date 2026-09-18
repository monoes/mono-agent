package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/storage"
)

// legacyOrgProjectRoot is where the CLI kept org state before the
// unification plan (C-31): orgs now resolve under the active profile's root,
// the same folder the GUI, chat tools, and org.run use. Orgs still found
// here are listed by `org legacy list` and moved by `org legacy move`.
const legacyOrgProjectRoot = "~/.monoagent"

// orgEnv resolves the org project root and, for DB-backed org commands, the
// profile and database. The DB opens lazily: plain monomind proxies with an
// explicit --project never touch it.
type orgEnv struct {
	cfg         *globalConfig
	projectFlag string

	once    sync.Once
	db      *storage.Database
	dbErr   error
	rootDir string
}

func (e *orgEnv) open() {
	e.once.Do(func() {
		e.db, e.dbErr = initDB(e.cfg)
		if e.dbErr != nil {
			return
		}
		if err := profiledir.EnsureLayout(e.db.DB, e.cfg.ProfileID); err != nil {
			e.dbErr = fmt.Errorf("preparing profile folder for %q: %w", e.cfg.ProfileID, err)
			return
		}
		e.rootDir = profiledir.Root(e.db.DB, e.cfg.ProfileID)
	})
}

// Root is the project root org proxies run against: --project when given,
// otherwise the active profile's root. When the database cannot be opened
// it falls back to the default per-profile folder rather than failing a
// read-only observe command.
func (e *orgEnv) Root() string {
	if e.projectFlag != "" {
		return expandPath(e.projectFlag)
	}
	e.open()
	if e.dbErr != nil {
		pid := e.cfg.ProfileID
		if pid == "" {
			pid = "default"
		}
		return profiledir.Root(nil, pid)
	}
	return e.rootDir
}

// Profile returns the database, profile id, and profile root for commands
// that read or write grants, endpoints, or autonomy rows. Those rows are
// keyed by profile, so an explicit --project pointing anywhere else is
// refused instead of silently editing one folder's org under another
// profile's grants (C-31).
func (e *orgEnv) Profile() (*storage.Database, string, string, error) {
	e.open()
	if e.dbErr != nil {
		return nil, "", "", e.dbErr
	}
	if e.projectFlag != "" {
		want, _ := filepath.Abs(e.rootDir)
		got, _ := filepath.Abs(expandPath(e.projectFlag))
		if filepath.Clean(want) != filepath.Clean(got) {
			return nil, "", "", fmt.Errorf("--project %s is not the org root of profile %q (%s); grants and autonomy belong to a profile — drop --project or pass --profile", got, e.cfg.ProfileID, want)
		}
	}
	return e.db, e.cfg.ProfileID, e.rootDir, nil
}

func (e *orgEnv) Close() {
	if e.db != nil {
		e.db.Close()
	}
}

// genOptions returns what reconcile needs to write generated org JSON
// blocks for this process.
func (e *orgEnv) genOptions(profileID string) orggrant.GenOptions {
	return orggrant.GenOptions{
		ProfileID: profileID,
		CLIPath:   selfExecutable(),
		APIAddr:   orgAPIAddr(e.db),
	}
}

// selfExecutable is the absolute, symlink-resolved path of this binary —
// the command monomind spawns for granted tool providers.
func selfExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return "monoagentcli"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe
}

// daemonAPIAddrSetting is the settings key `monoagentcli daemon` records
// its --api-addr under, so endpoint URLs written while the daemon is down
// still point where it will listen.
const daemonAPIAddrSetting = "daemon_api_addr"

// orgAPIAddr is the address endpoint URLs point at: MONOAGENT_API_ADDR,
// else a live daemon's heartbeat, else the address the daemon last
// recorded, else the default.
func orgAPIAddr(db *storage.Database) string {
	if v := os.Getenv("MONOAGENT_API_ADDR"); v != "" {
		return v
	}
	if hb, ok := daemonhb.Read(); ok && hb.APIAddr != "" {
		return hb.APIAddr
	}
	if db != nil {
		var v string
		if err := db.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, daemonAPIAddrSetting).Scan(&v); err == nil && v != "" {
			return v
		}
	}
	return orggrant.DefaultAPIAddr
}

// saveOrgReconciled reconciles doc against the enforcement rows and saves
// it. Every CLI path that writes an org JSON goes through here, so a doc
// never reaches disk carrying grants or providers no row backs.
func saveOrgReconciled(ctx context.Context, db *storage.Database, profileID, root string, doc *orgdesign.Doc, opts orggrant.GenOptions) (*orggrant.Report, error) {
	rep, err := orggrant.Reconcile(ctx, orggrant.NewStore(db.DB), doc, opts)
	if err != nil {
		return nil, err
	}
	auto, err := orgdecide.ReconcileAutonomy(ctx, orgdecide.NewStore(db.DB), profileID, doc)
	if err != nil {
		return nil, err
	}
	if auto.Lowered {
		rep.Findings = append(rep.Findings, orggrant.Finding{Kind: "autonomy_lowered", Detail: "lowered the enforced autonomy level to match the org file"})
	}
	if auto.Ignored != "" {
		rep.Findings = append(rep.Findings, orggrant.Finding{Kind: "autonomy_raise_ignored", Detail: auto.Ignored})
	}
	if _, err := orgdesign.Save(root, doc); err != nil {
		return rep, err
	}
	return rep, nil
}

// printJSONValue marshals v and prints it as one line.
func printJSONValue(v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return printOrgJSON(b)
}
