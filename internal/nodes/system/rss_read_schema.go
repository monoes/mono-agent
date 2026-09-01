package system

// RSSReadNodeSchema documents the config keys RSSReadNode.Execute reads out
// of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// This matches the hand-written schemas/system.rss_read.json field-for-field.
type RSSReadNodeSchema struct {
	URL string `json:"url" schema:"label=RSS/Atom Feed URL,type=text,required,placeholder=https://feeds.example.com/rss.xml"`

	Limit float64 `json:"limit" schema:"label=Max Items,type=number,default=20"`
}
