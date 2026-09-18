package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// checkWorkflowOrgReferences implements C-20: deleting a workflow that an
// org role is granted, or that answers as an automation role, is refused
// (exit 3) unless force is set. With force, the grants and endpoints are
// revoked first and every affected org's JSON is reconciled, so no role is
// left advertising a tool that can no longer run.
func checkWorkflowOrgReferences(ctx context.Context, cfg *globalConfig, workflowID string, force bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := initDB(cfg)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	store := orggrant.NewStore(db.DB)
	grants, err := store.GrantsForWorkflow(ctx, workflowID)
	if err != nil {
		return err
	}
	endpoints, err := store.EndpointsForWorkflow(ctx, workflowID)
	if err != nil {
		return err
	}
	if len(grants) == 0 && len(endpoints) == 0 {
		return nil
	}

	type orgKey struct{ profile, org string }
	affected := map[orgKey]bool{}
	var refs []string
	for _, g := range grants {
		affected[orgKey{g.ProfileID, g.OrgName}] = true
		refs = append(refs, fmt.Sprintf("grant to %s:%s", g.OrgName, g.RoleID))
	}
	for _, e := range endpoints {
		affected[orgKey{e.ProfileID, e.OrgName}] = true
		refs = append(refs, fmt.Sprintf("automation role %s:%s", e.OrgName, e.RoleID))
	}
	sort.Strings(refs)
	if !force {
		return errInvalidInput("workflow %q is used by %s; remove those first, or pass --force to revoke them and delete anyway", workflowID, strings.Join(refs, ", "))
	}

	for _, g := range grants {
		if t := g.Automation(); t != nil {
			if err := store.RevokeGrant(ctx, g.ProfileID, g.OrgName, g.RoleID, t.Alias); err != nil {
				return err
			}
		}
	}
	for _, e := range endpoints {
		if err := store.RevokeEndpoint(ctx, e.ProfileID, e.OrgName, e.RoleID); err != nil {
			return err
		}
	}
	for k := range affected {
		root := profiledir.Root(db.DB, k.profile)
		doc, err := orgdesign.Load(root, k.org)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: org %s: %v (grants were revoked; its JSON was not updated)\n", k.org, err)
			continue
		}
		rep, err := orggrant.Reconcile(ctx, store, doc, orggrant.GenOptions{ProfileID: k.profile, CLIPath: selfExecutable(), APIAddr: orgAPIAddr(db)})
		if err != nil {
			return err
		}
		if rep.Changed {
			if _, err := orgdesign.Save(root, doc); err != nil {
				fmt.Fprintf(os.Stderr, "warning: org %s: %v (grants were revoked; its JSON was not updated)\n", k.org, err)
			}
		}
	}
	fmt.Fprintf(os.Stderr, "revoked: %s\n", strings.Join(refs, ", "))
	return nil
}
