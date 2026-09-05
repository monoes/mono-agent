// internal/nodes/applications/list_test.go
package applicationsnodes_test

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	applicationsnodes "github.com/monoes/mono-agent/internal/nodes/applications"
	"github.com/monoes/mono-agent/internal/workflow"
)

func TestListNodeFiltersByKind(t *testing.T) {
	db := newTestDB(t)
	applicationsnodes.SetGlobalStore(db.DB)
	store := applications.NewStore(db.DB)

	job := &applications.Application{Kind: applications.KindJob, Job: &applications.JobDetails{Company: "Acme", URL: "https://a.example"}}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create job: %v", err)
	}
	tender := &applications.Application{Kind: applications.KindTender, Tender: &applications.TenderDetails{IssuingOrg: "Min", URL: "https://t.example", SubmissionDeadline: "2026-12-01"}}
	if err := store.Create(context.Background(), tender); err != nil {
		t.Fatalf("Create tender: %v", err)
	}

	node := &applicationsnodes.ListNode{}
	outputs, err := node.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{"kind": "job"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(outputs[0].Items) != 1 {
		t.Fatalf("expected 1 item for kind=job, got %d", len(outputs[0].Items))
	}
	if outputs[0].Items[0].JSON["id"] != job.ID {
		t.Fatalf("expected job %s, got %v", job.ID, outputs[0].Items[0].JSON["id"])
	}
}
