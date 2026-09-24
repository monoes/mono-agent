package workflow

import "context"

type triggerKey struct{}

type triggerInfo struct {
	triggerType string
	data        map[string]interface{}
}

// WithTrigger attaches an execution's trigger type and trigger data to ctx,
// so a node can read what started the run even when the nodes before it did
// not pass it on (org nodes continue the chain trace from it).
func WithTrigger(ctx context.Context, triggerType string, data map[string]interface{}) context.Context {
	return context.WithValue(ctx, triggerKey{}, triggerInfo{triggerType: triggerType, data: data})
}

// TriggerDataFrom returns the trigger data WithTrigger attached, or nil.
// Callers must not modify it.
func TriggerDataFrom(ctx context.Context) map[string]interface{} {
	info, _ := ctx.Value(triggerKey{}).(triggerInfo)
	return info.data
}

// TriggerTypeFrom returns the execution's trigger type WithTrigger attached:
// the trigger node's type for a trigger-fired run ("trigger.webhook",
// "trigger.org", …) or the org trigger type a run started from the org side
// was created with. It is set by mono-agent, never by the trigger's payload.
func TriggerTypeFrom(ctx context.Context) string {
	info, _ := ctx.Value(triggerKey{}).(triggerInfo)
	return info.triggerType
}

// OrgStarted reports whether a run with this trigger type was started by
// mono-agent's org side (a granted call, an automation-role message, a
// trigger.org event), the only runs whose trigger data carries a chain
// trace mono-agent wrote itself. A webhook body, a manual run's input or a
// schedule can put any `trace` object in their data.
func OrgStarted(triggerType string) bool {
	switch triggerType {
	// A trigger.org run is stored with its node type; org_event only ever
	// appears inside trigger data, so it is not an execution trigger type.
	case TriggerTypeOrgTool, TriggerTypeOrgMessage, TriggerNodeTypeOrg:
		return true
	}
	return false
}

// TriggerNodeTypeOrg is the trigger.org node type (orgbridge.TriggerNodeType,
// which this package cannot import), the trigger type of a run it fires.
const TriggerNodeTypeOrg = "trigger.org"
