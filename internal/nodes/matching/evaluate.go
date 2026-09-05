// Package matchingnodes exposes internal/matching as a workflow node
// type: applications.evaluate.
package matchingnodes

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/monoes/mono-agent/internal/matching"
	"github.com/monoes/mono-agent/internal/workflow"
)

var globalDB *sql.DB

// SetGlobalDB wires the shared SQLite connection into this package's node(s).
func SetGlobalDB(db *sql.DB) {
	globalDB = db
}

// RegisterAll registers applications.evaluate into the registry.
func RegisterAll(r *workflow.NodeTypeRegistry, db *sql.DB) {
	SetGlobalDB(db)
	r.Register("applications.evaluate", func() workflow.NodeExecutor { return &EvaluateNode{} })
}

// EvaluateNode scores one job application's fit via matching.Evaluate.
// Type: "applications.evaluate"
type EvaluateNode struct{}

func (n *EvaluateNode) Type() string { return "applications.evaluate" }

func (n *EvaluateNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	if globalDB == nil {
		return nil, fmt.Errorf("applications.evaluate: database not available (call SetGlobalDB at startup)")
	}
	applicationID, _ := config["application_id"].(string)
	if applicationID == "" {
		return nil, fmt.Errorf("applications.evaluate: config \"application_id\" is required")
	}
	runtime, _ := config["runtime"].(string)
	if runtime == "" {
		runtime = "claude"
	}
	profileID, _ := config["profile_id"].(string)
	if profileID == "" {
		profileID = "default"
	}

	verdict, err := matching.Evaluate(ctx, globalDB, profileID, applicationID, runtime)
	if err != nil {
		return nil, fmt.Errorf("applications.evaluate: %w", err)
	}

	out := map[string]interface{}{
		"application_id": applicationID, "verdict": verdict.Verdict, "overall_score": verdict.OverallScore,
		"rationale": verdict.Rationale,
	}
	return []workflow.NodeOutput{{Handle: "main", Items: []workflow.Item{workflow.NewItem(out)}}}, nil
}
