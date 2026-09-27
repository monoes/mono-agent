package workflow

import "testing"

// A workflow with no profile id is the default profile's (the rule the
// store and `workflow list` use), so another profile's engine must refuse
// it rather than treat it as everyone's.
func TestCheckWorkflowProfileUnownedMeansDefault(t *testing.T) {
	work := &WorkflowEngine{profileID: "p-work"}
	if err := work.checkWorkflowProfile(&Workflow{ID: "w"}); err == nil {
		t.Fatal("p-work ran a default-profile workflow")
	}
	if err := work.checkWorkflowProfile(&Workflow{ID: "w", ProfileID: "p-work"}); err != nil {
		t.Fatalf("own workflow refused: %v", err)
	}
	def := &WorkflowEngine{profileID: "default"}
	if err := def.checkWorkflowProfile(&Workflow{ID: "w"}); err != nil {
		t.Fatalf("default refused its own unowned workflow: %v", err)
	}
	all := &WorkflowEngine{profileID: "p-work", allowAllProfiles: true}
	if err := all.checkWorkflowProfile(&Workflow{ID: "w"}); err != nil {
		t.Fatalf("the daemon runs every profile: %v", err)
	}
}
