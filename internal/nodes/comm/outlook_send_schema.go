package comm

// OutlookSendNodeSchema documents the config keys OutlookSendNode.Execute
// reads out of its map[string]interface{} config, via `schema:"..."` tags
// consumed by internal/tools/schemagen (see that package's doc comment for
// the tag grammar). It is never constructed or used at runtime.
//
// See OutlookReadNodeSchema's doc comment for why the field key is
// "app_password" rather than "password".
//
// The hand-written schemas/comm.outlook_send.json this replaces already
// exposed cc, bcc, body_type, and attachments — those carry over unchanged.
// It did not expose in_reply_to/references, even though
// OutlookSendNode.Execute has read them (for reply threading, mirroring
// comm.email_send) since before this conversion, per the node's own doc
// comment. Generating from the struct surfaces that gap; the two fields are
// added here.
//
// credential_platform: outlook
type OutlookSendNodeSchema struct {
	Email string `json:"email" schema:"label=Outlook Email Address,type=text,required,placeholder=you@outlook.com"`

	AppPassword string `json:"app_password" schema:"label=App Password,type=password,required"`

	To string `json:"to" schema:"label=To Address(es),type=text,required,placeholder=user@example.com， other@example.com"`

	CC string `json:"cc" schema:"label=CC Address(es),type=text"`

	BCC string `json:"bcc" schema:"label=BCC Address(es),type=text"`

	Subject string `json:"subject" schema:"label=Subject,type=text,required"`

	Body string `json:"body" schema:"label=Email Body,type=textarea,required,rows=6"`

	BodyType string `json:"body_type" schema:"label=Body Type,type=select,default=text,options=text|html"`

	Attachments string `json:"attachments" schema:"label=Attachment File Paths,type=text"`

	InReplyTo string `json:"in_reply_to" schema:"label=In-Reply-To (Message-ID),type=text,help=Original message's Message-ID header (from comm.outlook_read) — threads this as a reply and prefixes the subject with \"Re: \"."`

	References string `json:"references" schema:"label=References,type=text,help=Thread's References header chain; defaults to In-Reply-To when omitted."`
}
