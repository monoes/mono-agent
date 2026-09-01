package comm

// TwilioNodeSchema documents the config keys TwilioNode.Execute reads out of
// its map[string]interface{} config, via `schema:"..."` tags consumed by
// internal/tools/schemagen (see that package's doc comment for the tag
// grammar). It is never constructed or used at runtime.
//
// Unlike the credential_platform nodes elsewhere in this package (bluesky,
// mastodon, reddit, ...), comm.twilio has no credential_id/credential_picker
// field — account_sid/auth_token are entered directly, matching the
// hand-written schema this replaces and TwilioNode.Execute, which reads
// them straight out of config rather than via credential_id resolution.
//
// credential_platform: twilio
type TwilioNodeSchema struct {
	AccountSID string `json:"account_sid" schema:"label=Account SID,type=text,required"`

	AuthToken string `json:"auth_token" schema:"label=Auth Token,type=password,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=send_sms|send_whatsapp|make_call,default=send_sms"`

	From string `json:"from" schema:"label=From Number,type=text,required,placeholder=+15551234567"`

	To string `json:"to" schema:"label=To Number,type=text,required,placeholder=+15557654321"`

	Body string `json:"body" schema:"label=Message,type=textarea,rows=3,depends_on_key=operation,depends_on_values=send_sms|send_whatsapp"`

	URL string `json:"url" schema:"label=TwiML URL,type=text,depends_on_key=operation,depends_on_values=make_call"`

	Twiml string `json:"twiml" schema:"label=Inline TwiML,type=textarea,rows=3,depends_on_key=operation,depends_on_values=make_call"`
}
