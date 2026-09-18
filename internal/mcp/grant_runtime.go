package mcp

import (
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// grantRuntime is the grant-mode runtime: database and workflow store only.
// The profile comes from the grant row, checked in grantScope.
func grantRuntime(db *storage.Database, opts Options) (*runtime, error) {
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
		profileID: opts.Profile,
		store:     workflow.NewHybridWorkflowStore(fileStore, workflow.NewSQLiteWorkflowStore(db.DB)),
	}, nil
}
