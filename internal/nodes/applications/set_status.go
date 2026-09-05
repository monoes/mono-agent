// internal/nodes/applications/set_status.go
package applicationsnodes

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/workflow"
)

// SetStatusNode transitions one application's status. Actor defaults to
// "system" since this runs inside an automated workflow.
// Type: "applications.set_status"
type SetStatusNode struct{}

func (n *SetStatusNode) Type() string { return "applications.set_status" }

func (n *SetStatusNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	if globalStore == nil {
		return nil, fmt.Errorf("applications.set_status: store not available (call SetGlobalStore at startup)")
	}
	id := configString(config, "id", "")
	if id == "" {
		return nil, fmt.Errorf("applications.set_status: config \"id\" is required")
	}
	status := configString(config, "status", "")
	profileID := configString(config, "profile_id", "default")
	actor := applications.Actor(configString(config, "actor", string(applications.ActorSystem)))
	note := configString(config, "note", "")

	if err := globalStore.SetStatus(ctx, profileID, id, applications.Status(status), actor, note); err != nil {
		return nil, fmt.Errorf("applications.set_status: %w", err)
	}

	out := map[string]interface{}{"id": id, "status": status}
	return []workflow.NodeOutput{{Handle: "main", Items: []workflow.Item{workflow.NewItem(out)}}}, nil
}
