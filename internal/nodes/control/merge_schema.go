package control

// MergeNodeSchema documents the config keys MergeNode.Execute reads out of
// its map[string]interface{} config — see SetNodeSchema's doc comment for
// why this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// MergeNode's own package doc comment mentions a "mode" config key
// ("append" (default) vs. "first"), but Execute never reads config at all —
// it unconditionally re-emits input.Items on the "main" handle exactly as
// the engine assembled it. "mode" is therefore dead documentation, not
// real behavior, and is intentionally not given a schema field here (there
// is nothing for it to configure). This struct has zero schema-tagged
// fields, matching the hand-written schemas/core.merge.json this replaces,
// which already had an empty "fields" array.
type MergeNodeSchema struct{}
