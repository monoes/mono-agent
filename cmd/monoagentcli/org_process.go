package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
)

func newOrgServeCmd(env *orgEnv) *cobra.Command {
	var foreground bool
	c := &cobra.Command{
		Use:   "serve",
		Short: "Start the org daemon (monomind org serve) for the active profile's folder",
		Long: "One org daemon per profile folder runs every org, delivers messages between them, and fires " +
			"scheduled orgs. Without --foreground it starts in the background (log: <folder>/.monomind/serve.log) " +
			"unless one is already running.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := env.Root()
			if foreground {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				return monomind.OrgServeRun(ctx, root)
			}
			pid, already, err := monomind.OrgServeStart(cmd.Context(), root)
			if err != nil {
				return err
			}
			status := "started"
			if already {
				status = "already-running"
			}
			return printJSONValue(map[string]interface{}{"v": 1, "root": root, "pid": pid, "status": status})
		},
	}
	c.Flags().BoolVar(&foreground, "foreground", false, "Run in this terminal until interrupted")
	return c
}

func newOrgLifecycleCmd(env *orgEnv, verb, short string, fn func(ctx context.Context, root, name string) (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <org>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := fn(cmd.Context(), env.Root(), args[0])
			if err != nil {
				return err
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": args[0], "ok": true, "output": out})
		},
	}
}

func newOrgSendCmd(env *orgEnv) *cobra.Command {
	var to, from, subject, body string
	c := &cobra.Command{
		Use:   "send <org>",
		Short: "Send a message to an org role (live when the org runs, queued for its next start otherwise)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			org := args[0]
			if !orgdesign.ValidOrgName(org) {
				return errInvalidInput("invalid org name %q", org)
			}
			if from != "" && !strings.Contains(from, ":") {
				return errInvalidInput("--from must be qualified as <org>:<role>")
			}
			db, profileID, root, err := env.Profile()
			if err != nil {
				return err
			}
			tr, _ := orgbridge.ParseTrace(body)
			res, err := orgbridge.Send(cmd.Context(), orgbridge.NewLedger(db.DB), orgbridge.SendRequest{
				ProfileID: profileID, Root: root, Org: org, To: to, From: from, Subject: subject,
				Body: orgbridge.StripTrace(body), Trace: tr, Direction: orgbridge.DirWorkflowOut,
			})
			if err != nil {
				var refused *orgbridge.ErrRefused
				if errors.As(err, &refused) {
					return errInvalidInput("%s", refused.Error())
				}
				return err
			}
			return printJSONValue(map[string]interface{}{
				"v": 1, "org": org, "to": res.To, "from": res.From, "delivery": res.Delivery,
				"receipt": res.Receipt, "messageId": res.MessageID,
				"trace": map[string]interface{}{"chain_id": res.Trace.ChainID, "hop": res.Trace.Hop},
			})
		},
	}
	c.Flags().StringVar(&to, "to", "", "Role id (default: the org's coordinator)")
	c.Flags().StringVar(&from, "from", "human:operator", "Sender as <org>:<role>")
	c.Flags().StringVar(&subject, "subject", "", "Subject")
	c.Flags().StringVar(&body, "body", "", "Message body")
	_ = c.MarkFlagRequired("body")
	return c
}

// orgNameInUse reports the other profile (if any) whose folder already has
// an org named name — org names are unique per machine because monomind's
// broker is keyed by bare name (U9, C-9).
func orgNameInUse(db *sql.DB, name, exceptProfile string) (string, error) {
	rows, err := db.Query(`SELECT id FROM profiles`)
	if err != nil {
		return "", err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if !containsString(ids, "default") {
		ids = append(ids, "default")
	}
	for _, id := range ids {
		if id == exceptProfile {
			continue
		}
		root := profiledir.Root(db, id)
		if root == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(orgdesign.OrgsDir(root), name+".json")); err == nil {
			return id, nil
		}
	}
	return "", nil
}

func newOrgRenameCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <org> <new-name>",
		Short: "Rename a stopped org, moving its data, grants, endpoints, and decisions",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			oldName, newName := args[0], args[1]
			if !orgdesign.ValidOrgName(newName) {
				return errInvalidInput("invalid org name %q", newName)
			}
			db, profileID, root, doc, err := loadOrgForEdit(env, oldName)
			if err != nil {
				return err
			}
			dir := orgdesign.OrgsDir(root)
			if _, err := os.Stat(filepath.Join(dir, newName+".json")); err == nil {
				return errInvalidInput("org %q already exists", newName)
			}
			if other, err := orgNameInUse(db.DB, newName, profileID); err != nil {
				return err
			} else if other != "" {
				return errInvalidInput("org name %q is already used in profile %q; org names are unique on this machine — try %s-%s", newName, other, newName, shortProfile(profileID))
			}
			if running, _ := legacyOrgRunning(cmd, root, oldName); running {
				return errInvalidInput("org %q is running; stop it before renaming", oldName)
			}
			paths, err := legacyOrgPaths(dir, oldName)
			if err != nil {
				return err
			}
			if err := orggrant.NewStore(db.DB).RenameOrg(ctx, profileID, oldName, newName); err != nil {
				return err
			}
			for _, p := range paths {
				if p == oldName+".json" {
					continue
				}
				np := newName + strings.TrimPrefix(p, oldName)
				if err := os.Rename(filepath.Join(dir, p), filepath.Join(dir, np)); err != nil {
					return fmt.Errorf("moving %s: %w", p, err)
				}
			}
			doc.Name = newName
			if _, err := saveOrgReconciled(ctx, db, profileID, root, doc, env.genOptions(profileID)); err != nil {
				return err
			}
			if err := os.Remove(filepath.Join(dir, oldName+".json")); err != nil && !os.IsNotExist(err) {
				return err
			}
			// Holding orgs that listed the old name follow the rename.
			docs, _, _ := orgdesign.LoadAll(root)
			for _, d := range docs {
				changed := false
				for i := range d.ChildOrgs {
					if d.ChildOrgs[i].Org == oldName {
						d.ChildOrgs[i].Org = newName
						changed = true
					}
				}
				if changed {
					if _, err := orgdesign.Save(root, d); err != nil {
						return fmt.Errorf("updating holding org %s: %w", d.Name, err)
					}
				}
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": oldName, "renamed_to": newName})
		},
	}
}

func shortProfile(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

func newOrgDeleteCmd(env *orgEnv) *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "delete <org>",
		Short: "Delete an org and all its data, revoking its grants and automation-role endpoints",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]
			db, profileID, root, err := env.Profile()
			if err != nil {
				return err
			}
			docs, _, _ := orgdesign.LoadAll(root)
			if owner := orgdesign.HoldingOwner(docs, name); owner != "" && !force {
				return errInvalidInput("org %q is a child of holding org %q; remove it there first or pass --force", name, owner)
			}
			if _, err := monomind.OrgDelete(ctx, root, name, force); err != nil {
				return err
			}
			if err := orggrant.NewStore(db.DB).RevokeOrg(ctx, profileID, name); err != nil {
				return err
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": name, "deleted": true})
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Delete even when the org appears to be running or belongs to a holding org")
	return c
}

// statusDaemons is the `status --json` daemon block (contracts §4).
func statusDaemons(root string) map[string]interface{} {
	hb, live := daemonHeartbeat()
	daemon := map[string]interface{}{"running": live, "pid": nil, "api_addr": nil, "heartbeat_age_ms": nil}
	if hb != nil && hb.PID > 0 {
		daemon["pid"] = hb.PID
		if hb.APIAddr != "" {
			daemon["api_addr"] = hb.APIAddr
		}
		daemon["heartbeat_age_ms"] = hb.AgeMS
	}
	serve := map[string]interface{}{"running": false, "pid": nil, "root": root}
	if shb, ok := monomind.ReadServeHeartbeat(root); shb != nil {
		serve["running"] = ok
		serve["pid"] = shb.PID
	}
	return map[string]interface{}{"v": 1, "daemon": daemon, "org_serve": serve}
}

type heartbeatInfo struct {
	PID     int
	APIAddr string
	AgeMS   int64
}

func daemonHeartbeat() (*heartbeatInfo, bool) {
	hb, live := daemonhb.Read()
	if hb.PID <= 0 {
		return nil, false
	}
	return &heartbeatInfo{PID: hb.PID, APIAddr: hb.APIAddr, AgeMS: time.Since(hb.TS).Milliseconds()}, live
}
