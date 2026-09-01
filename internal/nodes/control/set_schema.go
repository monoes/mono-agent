package control

// SetNodeSchema documents the config keys SetNode.Execute reads out of its
// map[string]interface{} config, via `schema:"..."` tags consumed by
// internal/tools/schemagen (see that package's doc comment for the tag
// grammar). It is never constructed or used at runtime — Execute keeps
// reading the raw map exactly as before; this struct exists solely to
// generate internal/workflow/schemas/core.set.json.
type SetNodeSchema struct {
	Assignments string `json:"assignments" schema:"label=Field Assignments (JSON),type=textarea,required,rows=5,placeholder=[\n  { \"field\": \"fullName\"， \"value\": \"{{ $json.first }} {{ $json.last }}\" }，\n  { \"field\": \"amount\"， \"value\": \"{{ $json.total }}\"， \"type\": \"number\" }\n],help=JSON array of assignment objects， each with \"field\" (dot-path to set， e.g. \"a.b.c\")， \"value\" (template expression)， and optional \"type\": \"string\" (default)， \"number\"， \"bool\"， or \"json\"."`

	IncludeInput bool `json:"include_input" schema:"label=Include Input Fields,type=boolean,default=true,help=If false， output items contain only the assigned fields."`
}
