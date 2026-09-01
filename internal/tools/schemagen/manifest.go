package schemagen

// ManifestEntry maps one workflow node type to the Go struct that generates
// its schema (see the package doc comment for why this is a companion
// struct rather than the node's runtime config).
type ManifestEntry struct {
	// NodeType is the workflow node type string, e.g. "core.set". The
	// generator writes internal/workflow/schemas/<NodeType>.json.
	NodeType string
	// GoFile is the source file to parse, relative to the repo root.
	GoFile string
	// StructName is the schema-tagged struct to read from GoFile.
	StructName string
}

// Manifest lists every node type currently opted into generated schemas.
// Adding an entry here is step 2 of converting a node type — see
// internal/workflow/schemas/README.md for the full procedure.
var Manifest = []ManifestEntry{
	{
		NodeType:   "core.set",
		GoFile:     "internal/nodes/control/set_schema.go",
		StructName: "SetNodeSchema",
	},
	{
		NodeType:   "core.filter",
		GoFile:     "internal/nodes/control/filter_schema.go",
		StructName: "FilterNodeSchema",
	},
	{
		NodeType:   "core.if",
		GoFile:     "internal/nodes/control/if_schema.go",
		StructName: "IfNodeSchema",
	},
	{
		NodeType:   "http.request",
		GoFile:     "internal/nodes/http/request_schema.go",
		StructName: "RequestNodeSchema",
	},
}
