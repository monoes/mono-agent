package comm

// EmailSendNodeSchema documents the config keys EmailSendNode.Execute reads
// out of its map[string]interface{} config, via `schema:"..."` tags
// consumed by internal/tools/schemagen (see that package's doc comment for
// the tag grammar). It is never constructed or used at runtime.
//
// The hand-written schemas/comm.email_send.json this replaces exposes only
// smtp_host, smtp_port, username, password, from, to, subject, body, and a
// legacy "html" boolean. EmailSendNode.Execute has supported cc, bcc,
// body_type (preferred over "html" — see the fallback order at the top of
// Execute), attachments, in_reply_to, and references since before this
// conversion, per the node's own doc comment, but the hand-written schema
// never surfaced a way to set any of them from the UI. Generating from the
// struct (rather than hand-copying the old JSON) surfaces that gap: this
// schema exposes "body_type" as the primary select in place of the old
// "html" checkbox (Execute already prefers body_type when both are set, so
// this doesn't change runtime behavior, only what the UI offers), plus the
// previously-hidden cc/bcc/attachments/in_reply_to/references fields.
//
// credential_platform: smtp
type EmailSendNodeSchema struct {
	SMTPHost string `json:"smtp_host" schema:"label=SMTP Host,type=text,required,placeholder=smtp.gmail.com"`

	SMTPPort float64 `json:"smtp_port" schema:"label=SMTP Port,type=number,required,default=587"`

	Username string `json:"username" schema:"label=Username,type=text,required"`

	Password string `json:"password" schema:"label=Password / App Password,type=password,required"`

	From string `json:"from" schema:"label=From Address,type=text,required,placeholder=you@example.com"`

	To string `json:"to" schema:"label=To Address(es),type=text,required,placeholder=user@example.com， other@example.com"`

	CC string `json:"cc" schema:"label=CC Address(es),type=text"`

	BCC string `json:"bcc" schema:"label=BCC Address(es),type=text"`

	Subject string `json:"subject" schema:"label=Subject,type=text,required"`

	Body string `json:"body" schema:"label=Email Body,type=textarea,required,rows=6"`

	BodyType string `json:"body_type" schema:"label=Body Type,type=select,options=text|html,default=text"`

	Attachments string `json:"attachments" schema:"label=Attachment File Paths,type=text"`

	InReplyTo string `json:"in_reply_to" schema:"label=In-Reply-To (Message-ID),type=text,help=Original message's Message-ID header (from comm.email_read) — threads this as a reply and prefixes the subject with \"Re: \"."`

	References string `json:"references" schema:"label=References,type=text,help=Thread's References header chain; defaults to In-Reply-To when omitted."`
}
