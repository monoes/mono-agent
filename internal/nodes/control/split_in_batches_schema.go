package control

// SplitInBatchesNodeSchema documents the config keys
// SplitInBatchesNode.Execute reads out of its map[string]interface{}
// config — see SetNodeSchema's doc comment for why this is a companion
// struct rather than the runtime config, and internal/tools/schemagen for
// the tag grammar.
//
// Two drift findings vs. the hand-written schemas/core.split_in_batches.json
// this replaces:
//
//   - The hand-written schema had no "max" bound on batch_size; Execute
//     actually enforces batch_size in [1, 1000]. Added here.
//   - The hand-written schema exposed a "reset" boolean ("Reset on new
//     execution", default true). Execute's own doc comment says as much:
//     "not used at node level, stateless here" — the code never reads
//     config["reset"] at all. It is dead UI, not real behavior, and is
//     dropped here rather than perpetuated.
type SplitInBatchesNodeSchema struct {
	BatchSize float64 `json:"batch_size" schema:"label=Batch Size,type=number,required,default=10,min=1,max=1000"`
}
