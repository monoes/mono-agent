package data

// CryptoNodeSchema documents the config keys CryptoNode.Execute reads out of
// its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// The hand-written schemas/data.crypto.json this replaces was missing three
// operations Execute has always supported ("uuid", "random_bytes", plus the
// "output_field"/"encoding"/"length" fields those and the hash/hmac
// operations read) — CryptoNode.Execute never exposed a way to generate a
// UUID or random bytes, or to pick base64 vs hex output encoding, through
// the UI. Generating from the struct surfaces that gap; it's fixed here by
// adding those fields rather than silently carrying the gap forward.
//
// Execute also accepts a "key" config alias for "secret" (checked only when
// "secret" is empty) — that's an undocumented alias for the same field, not
// a distinct config value, so it isn't given its own schema entry.
type CryptoNodeSchema struct {
	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=hash_md5|hash_sha256|hash_sha512|hmac_sha256|uuid|random_bytes|base64_encode|base64_decode,default=hash_sha256"`

	Field string `json:"field" schema:"label=Input Field,type=text,placeholder=password,help=Item field to read input from. Not used for uuid/random_bytes.,depends_on_key=operation,depends_on_values=hash_md5|hash_sha256|hash_sha512|hmac_sha256|base64_encode|base64_decode"`

	Secret string `json:"secret" schema:"label=HMAC Secret,type=password,depends_on_key=operation,depends_on_values=hmac_sha256"`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,help=Defaults to the input field， overwriting it."`

	Encoding string `json:"encoding" schema:"label=Output Encoding,type=select,options=hex|base64,default=hex,help=Encoding used for hash/hmac/random_bytes output.,depends_on_key=operation,depends_on_values=hash_md5|hash_sha256|hash_sha512|hmac_sha256|random_bytes"`

	Length float64 `json:"length" schema:"label=Random Byte Count,type=number,default=16,depends_on_key=operation,depends_on_values=random_bytes"`
}
