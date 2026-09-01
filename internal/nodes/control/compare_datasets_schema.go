package control

// CompareDatasetsNodeSchema documents the config keys
// CompareDatasetsNode.Execute reads out of its map[string]interface{}
// config — see SetNodeSchema's doc comment for why this is a companion
// struct rather than the runtime config, and internal/tools/schemagen for
// the tag grammar.
//
// CompareDatasetsNode's own package doc comment additionally mentions
// "dataset_a"/"dataset_b" config keys ("label for first/second dataset"),
// but Execute never reads config["dataset_a"] or config["dataset_b"] — the
// output handles are the fixed strings "added"/"removed"/"changed"/
// "unchanged" regardless of config. Those two keys are omitted here since
// they have no effect; only "key_field" and "split_at" are read.
type CompareDatasetsNodeSchema struct {
	KeyField string `json:"key_field" schema:"label=Key Field,type=text,required,placeholder=id,help=Field used as the unique identifier when matching items between the two datasets."`

	SplitAt float64 `json:"split_at" schema:"label=Split Index,type=number,help=Index at which the incoming items are split into dataset A (before) and dataset B (from this index on). Defaults to a 50/50 split."`
}
