package vault

// SecretGetNodeSchema documents the config keys SecretGetNode.Execute reads
// out of its map[string]interface{} config — see internal/tools/schemagen's
// doc comment for the tag grammar.
type SecretGetNodeSchema struct {
	Name string `json:"name" schema:"label=Name,type=text,required,help=Name of the vault entry to decrypt (as shown in the Vault page / monoagentcli secret list)."`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,default=credential,help=Item field the decrypted fields object is written to. WARNING: this puts the plaintext secret into the item stream — it is stored unmasked in execution history and flows to every downstream node."`
}
