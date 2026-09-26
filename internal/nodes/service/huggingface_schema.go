package service

// HuggingFaceNodeSchema documents the config keys HuggingFaceNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar. It is never constructed or
// used at runtime; this struct exists solely to generate
// internal/workflow/schemas/service.huggingface.json.
//
// credential_platform: huggingface
type HuggingFaceNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=HuggingFace Account,type=credential_picker,required"`

	// generate_text was removed (LLM inference runs through agent.ask).
	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=generate_image,default=generate_image"`

	Prompt string `json:"prompt" schema:"label=Prompt,type=text,required,help=The prompt. Supports {{ $json.fieldName }} expressions."`

	Model string `json:"model" schema:"label=Model,type=text,help=Default: black-forest-labs/FLUX.1-schnell."`
}
