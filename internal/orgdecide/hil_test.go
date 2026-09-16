package orgdecide

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
)

// Q12: a HIL item inside a run the org started through a grant is that
// org's decision at the grant's tier; a standalone workflow's HIL item is
// never touched.
func TestHILItemsOfOrgRunsAreRouted(t *testing.T) {
	org := &fakeOrg{}
	dec := &scriptedDecider{replies: map[string]string{KindApproval: `{"verdict":"approve","rationale":"looks right"}`}}
	s := newTestService(t, org, dec)
	ctx := context.Background()
	db := s.DB
	mustExec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO workflows (id, name, profile_id) VALUES ('wf', 'Publish', 'p')`)
	mustExec(`INSERT INTO workflow_executions (id, workflow_id, status, trigger_type, trigger_data, profile_id) VALUES ('e-org', 'wf', 'WAITING', 'org_tool', '{"org":{"name":"growth","role":"writer","automation":"publish"}}', 'p')`)
	mustExec(`INSERT INTO workflow_executions (id, workflow_id, status, trigger_type, profile_id) VALUES ('e-solo', 'wf', 'WAITING', 'trigger.manual', 'p')`)
	mustExec(`INSERT INTO hil_pending (id, execution_id, workflow_id, node_id, node_name, readonly_data, profile_id) VALUES ('hil-org', 'e-org', 'wf', 'n', 'Review post', '{"text":"hello"}', 'p')`)
	mustExec(`INSERT INTO hil_pending (id, execution_id, workflow_id, node_id, node_name, profile_id) VALUES ('hil-solo', 'e-solo', 'wf', 'n', 'Review', 'p')`)
	if _, err := orggrant.NewStore(db).UpsertGrant(ctx, orggrant.GrantInput{ProfileID: "p", OrgName: "growth", RoleID: "writer",
		Tool: orggrant.Tool{Alias: "publish", WorkflowID: "wf", Tier: orgdesign.TierConsequential}}); err != nil {
		t.Fatal(err)
	}

	setLevel(t, s, orgdesign.LevelMid)
	ds, err := s.ProcessOrg(ctx, "p", t.TempDir(), "growth")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].ItemRef != "hil-org" || ds[0].Class != "hil:publish" || ds[0].Tier != "consequential" || ds[0].Verdict != VerdictApproved {
		t.Fatalf("decisions = %+v", ds)
	}
	var orgStatus, soloStatus string
	_ = db.QueryRow(`SELECT status FROM hil_pending WHERE id = 'hil-org'`).Scan(&orgStatus)
	_ = db.QueryRow(`SELECT status FROM hil_pending WHERE id = 'hil-solo'`).Scan(&soloStatus)
	if orgStatus != "approved" || soloStatus != "pending" {
		t.Fatalf("hil statuses: org=%s solo=%s", orgStatus, soloStatus)
	}
}
