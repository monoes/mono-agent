package httpnodes

// RequestNodeSchema documents the config keys RequestNode.Execute reads out
// of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// This intentionally covers only the fields present in the hand-written
// schemas/http.request.json it replaces — RequestNode.Execute also reads
// several auth_*, pagination_*, body_type, and response_format keys that
// the hand-written schema never exposed either; extending schema coverage
// for those is a separate follow-up, not part of converting this file's
// existing shape to generated output.
type RequestNodeSchema struct {
	URL string `json:"url" schema:"label=URL,type=text,required,placeholder=https://api.example.com/data"`

	Method string `json:"method" schema:"label=Method,type=select,required,options=GET|POST|PUT|PATCH|DELETE|HEAD,default=GET"`

	Headers string `json:"headers" schema:"label=Headers (JSON),type=textarea,rows=3,placeholder={ \"Authorization\": \"Bearer {{$env.API_KEY}}\" }"`

	Body string `json:"body" schema:"label=Request Body,type=textarea,rows=5,depends_on_key=method,depends_on_values=POST|PUT|PATCH"`

	Timeout float64 `json:"timeout" schema:"label=Timeout (seconds),type=number,default=30"`

	MaxBodyMB float64 `json:"max_body_mb" schema:"label=Max Response Body (MB),type=number,default=64,help=Maximum response body size in megabytes (default 64). Larger responses fail with an error instead of exhausting memory."`
}
