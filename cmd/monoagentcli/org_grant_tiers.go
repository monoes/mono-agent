package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// grantWorkflowLoader reads a granted workflow as it is now (file first,
// as execution does), for orggrant.RaiseTiers and the decision service. The
// store is opened on first use; the loader is safe for concurrent use.
func grantWorkflowLoader(db *storage.Database) orggrant.WorkflowLoader {
	var once sync.Once
	var store *workflow.HybridWorkflowStore
	return func(ctx context.Context, id string) (*workflow.Workflow, error) {
		once.Do(func() { store = newHybridStore(db) })
		return store.GetWorkflow(ctx, id)
	}
}

// raiseGrantTiersForWorkflow runs after a workflow is saved: every grant of
// it whose stored tier the workflow has outgrown is raised (#284), each
// raise is noted on stderr, and the orgs involved have their files
// reconciled so the grant's display copy shows the new tier.
func raiseGrantTiersForWorkflow(ctx context.Context, db *storage.Database, load orggrant.WorkflowLoader, workflowID string) {
	store := orggrant.NewStore(db.DB)
	grants, err := store.GrantsForWorkflow(ctx, workflowID)
	if err != nil || len(grants) == 0 {
		return
	}
	raised, err := store.RaiseTiers(ctx, grants, load)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: workflow %s: re-deriving grant tiers: %v\n", workflowID, err)
	}
	type orgKey struct{ profile, org string }
	affected := map[orgKey]bool{}
	for _, r := range raised {
		fmt.Fprintf(os.Stderr, "notice: org %s: %s\n", r.Org, r)
		affected[orgKey{r.Profile, r.Org}] = true
	}
	for k := range affected {
		root := profiledir.Root(db.DB, k.profile)
		doc, err := orgdesign.Load(root, k.org)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: org %s: %v (the grant tier was raised; its JSON was not updated)\n", k.org, err)
			continue
		}
		opts := orggrant.GenOptions{ProfileID: k.profile, CLIPath: selfExecutable(), APIAddr: orgAPIAddr(db), Workflow: load}
		if _, err := saveOrgReconciled(ctx, db, k.profile, root, doc, opts); err != nil {
			fmt.Fprintf(os.Stderr, "warning: org %s: %v (the grant tier was raised; its JSON was not updated)\n", k.org, err)
		}
	}
}
