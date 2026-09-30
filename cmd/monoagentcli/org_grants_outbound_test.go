package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/nodes"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// A grant to a workflow that sends LinkedIn DMs defaults to
// required/irreversible, so the decider can't approve its calls at mid
// (#287); a LinkedIn scrape stays consequential when the installed
// official package verifies it as a read.
func TestOrgGrantLinkedInDMIsIrreversible(t *testing.T) {
	f := newOrgCLIFixture(t)
	if _, err := nodes.BootAutomations(filepath.Join(os.Getenv("HOME"), ".monoagent")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { action.SetDefSource(nil); action.GetLoader().InvalidateAll() })
	scrapeApproval, scrapeTier, scrapeOutbound := "none", "consequential", false
	if !bot.PlatformCompiledIn("linkedin") {
		scrapeApproval, scrapeTier, scrapeOutbound = "required", "irreversible", true
	}
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	store := workflow.NewSQLiteWorkflowStore(db.DB)
	mk := func(name, nodeType string) string {
		ctx := context.Background()
		wf := &workflow.Workflow{Name: name, ProfileID: "default"}
		if err := store.CreateWorkflow(ctx, wf); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveWorkflowNodes(ctx, wf.ID, []workflow.WorkflowNode{
			{ID: wf.ID + "-t", WorkflowID: wf.ID, Type: "trigger.manual", Name: "start"},
			{ID: wf.ID + "-n", WorkflowID: wf.ID, Type: nodeType, Name: "step"},
		}); err != nil {
			t.Fatal(err)
		}
		return wf.ID
	}
	dmWF := mk("DM leads", "linkedin.send_dms")
	scrapeWF := mk("Scrape leads", "linkedin.scrape_profile_info")
	db.Close()

	for _, c := range []struct {
		wf, alias, approval, tier string
		outbound                  bool
	}{
		{dmWF, "dm_leads", "required", "irreversible", true},
		{scrapeWF, "scrape_leads", scrapeApproval, scrapeTier, scrapeOutbound},
	} {
		add := f.mustRun(t, "automation", "add", "growth", "--workflow", c.wf, "--alias", c.alias, "--owned")
		if got := add["automation"].(map[string]interface{})["has_outbound_nodes"]; got != c.outbound {
			t.Errorf("%s: has_outbound_nodes = %v", c.alias, got)
		}
		g := f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", c.alias)
		grant := g["grant"].(map[string]interface{})
		if grant["approval"] != c.approval || grant["tier"] != c.tier {
			t.Errorf("%s: grant = approval %v, tier %v; want %s, %s", c.alias, grant["approval"], grant["tier"], c.approval, c.tier)
		}
	}
}
