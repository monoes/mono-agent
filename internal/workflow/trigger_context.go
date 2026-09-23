package workflow

import "context"

type triggerDataKey struct{}

// WithTriggerData attaches an execution's trigger data to ctx, so a node can
// read what started the run even when the nodes before it did not pass it
// on (org nodes continue the chain trace from it).
func WithTriggerData(ctx context.Context, td map[string]interface{}) context.Context {
	if td == nil {
		return ctx
	}
	return context.WithValue(ctx, triggerDataKey{}, td)
}

// TriggerDataFrom returns the trigger data WithTriggerData attached, or nil.
// Callers must not modify it.
func TriggerDataFrom(ctx context.Context) map[string]interface{} {
	td, _ := ctx.Value(triggerDataKey{}).(map[string]interface{})
	return td
}
