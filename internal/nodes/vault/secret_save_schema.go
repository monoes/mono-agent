package vault

// SecretSaveNodeSchema documents the config keys SecretSaveNode.Execute
// reads out of its map[string]interface{} config — see
// internal/tools/schemagen's doc comment for the tag grammar.
type SecretSaveNodeSchema struct {
	Kind string `json:"kind" schema:"label=Kind,type=select,options=secret|login,default=secret,help=Vault entry kind — secret (arbitrary fields) or login (has username/url)."`

	Name string `json:"name" schema:"label=Name,type=text,required,help=Name to save the vault entry under. Must be unique per profile — saving again under the same name overwrites nothing (fails); use vault.secret_get to check first if needed. This value is the same for every item in a batch — running this node against more than one item saves the first and fails on the second (duplicate name)， so batches need a per-item-unique name."`

	FieldKeys string `json:"field_keys" schema:"label=Fields To Save (JSON array),type=textarea,required,rows=2,placeholder=[\"api_key\"， \"refresh_token\"],help=JSON array of item field names whose CURRENT values are encrypted and stored under those same names."`

	Username string `json:"username" schema:"label=Username,type=text,help=Optional username to store alongside the entry (login kind)."`

	URL string `json:"url" schema:"label=URL,type=text,help=Optional URL to store alongside the entry (login kind)."`

	Notes string `json:"notes" schema:"label=Notes,type=textarea,rows=2,help=Optional free-text notes， encrypted alongside the fields."`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,default=vault_id,help=Item field where the new vault entry id (e.g. sec-001) is written."`
}
