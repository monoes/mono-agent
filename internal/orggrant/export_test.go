package orggrant

import "github.com/monoes/mono-agent/internal/workflow"

// IsOutboundNode exposes the classifier to the registry walk in
// outbound_registry_test.go (package orggrant_test: noderegistry imports
// orggrant).
func IsOutboundNode(n workflow.WorkflowNode) bool { return isOutboundNode(n) }

// Classified reports whether nodeType was reviewed: read-only, read-only
// by config or by its installed definition, or matched by an explicit
// outbound rule.
func Classified(nodeType string) bool {
	_, byConfig := readOnlyByConfig[nodeType]
	_, byDef := readOnlyActions[nodeType]
	return readOnlyNodes[nodeType] || byConfig || byDef || classifiedOutbound(nodeType)
}

// ReadOnlyActions lists readOnlyActions.
func ReadOnlyActions() map[string][]string { return readOnlyActions }
