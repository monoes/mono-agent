package control

// CodeNodeSchema documents the config keys CodeNode.Execute reads out of its
// map[string]interface{} config — see SetNodeSchema's doc comment for why
// this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// Two drift findings vs. the hand-written schemas/core.code.json this
// replaces:
//
//   - The hand-written schema had a required "language" select
//     (javascript|python, default javascript). CodeNode.Execute never reads
//     config["language"] at all — it always runs the code through the goja
//     JS runtime, regardless of what the UI field said. Selecting "python"
//     would have silently run the code as JavaScript. That field is dropped
//     here rather than perpetuated; there is no config key it maps to.
//   - "timeout_seconds" and "memory_limit_mb" are real, actually-read config
//     keys (execution timeout default 30s clamped to 1-600; goja memory
//     limit default 512MB) that had no field in the hand-written schema at
//     all.
type CodeNodeSchema struct {
	Code string `json:"code" schema:"label=Code,type=code,required,language=javascript,help=Return an array of objects. Each object becomes an output item. Access input via $input.all() or $json (first item)."`

	TimeoutSeconds float64 `json:"timeout_seconds" schema:"label=Timeout (seconds),type=number,default=30,min=1,max=600,help=Execution timeout in seconds."`

	MemoryLimitMB float64 `json:"memory_limit_mb" schema:"label=Memory Limit (MB),type=number,default=512,min=1,help=JS runtime memory limit in megabytes."`
}
