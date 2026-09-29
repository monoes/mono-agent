package orggrant

import (
	"strings"

	"github.com/monoes/mono-agent/internal/workflow"
)

// reservedTriggerKeys are the trigger-data fields mono-agent itself sets on
// a run an org starts (a granted call, an automation role's message, a
// trigger.org event). A role's arguments never land on them: org carries
// the workdir file nodes confine to, trace the chain the run signs.
var reservedTriggerKeys = map[string]bool{
	"trigger_type":           true,
	"org":                    true,
	"input":                  true,
	"trace":                  true,
	workflow.WebhookTraceKey: true,
	"org_message":            true,
	"org_event":              true,
}

// IsReservedTriggerKey reports whether key is a field an org-started run's
// trigger data reserves: one of reservedTriggerKeys, or any key starting
// with "_" (internal markers such as _jev).
func IsReservedTriggerKey(key string) bool {
	return reservedTriggerKeys[key] || strings.HasPrefix(key, "_")
}

// LiftInput copies each field of input (the role's arguments, when they are
// an object) to the top level of data, so a workflow written for a manual
// `workflow run --input '{"keywords":…}'`, which reads {{ $json.keywords }},
// gets its arguments as a granted tool too. {{ $json.input.<field> }} keeps
// working. A reserved key or one data already has is never overwritten.
func LiftInput(data map[string]interface{}, input interface{}) {
	fields, ok := input.(map[string]interface{})
	if !ok || data == nil {
		return
	}
	for k, v := range fields {
		if IsReservedTriggerKey(k) {
			continue
		}
		if _, exists := data[k]; exists {
			continue
		}
		data[k] = v
	}
}
