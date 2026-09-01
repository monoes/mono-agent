package service

// OutlookMailNodeSchema documents the config keys OutlookMailNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// Several real gaps between Execute and the hand-written
// schemas/service.outlook_mail.json this replaces were found while writing
// this struct, all fixed here rather than silently carried forward:
//
//   - "create_reply" and "reply" are operations Execute has supported since
//     it was written (see the "create_reply", "reply" case in Execute) but
//     the hand-written schema's operation options list never included them.
//   - "cc", "bcc", and "reply_all" are config keys Execute reads
//     (parseEmailAddresses(config["cc"]/config["bcc"]), boolVal(config,
//     "reply_all")) for send_message/create_draft/create_reply/reply, but
//     had no schema field at all.
//   - "download_attachments" is a config key Execute reads
//     (downloadAttachmentsEnabled) for list_messages and get_message, but
//     had no schema field at all.
//   - "mailbox" is also read for get_message (enrichOutlookMessage), not
//     just list_messages as the old schema's depends_on implied.
//   - "message_id", "to", and "body" are also read for the create_reply/
//     reply operations, not just the operations the old schema's
//     depends_on listed.
//
// credential_platform: outlook
type OutlookMailNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Outlook Account,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=list_messages|get_message|send_message|create_draft|create_reply|reply|send_draft|delete_message|whoami,default=list_messages"`

	Mailbox string `json:"mailbox" schema:"label=Mailbox,type=text,default=inbox,help=Any Outlook well-known folder name: inbox， drafts， sentitems， deleteditems， junkemail， archive， outbox.,depends_on_key=operation,depends_on_values=list_messages|get_message"`

	Search string `json:"search" schema:"label=Search,type=text,help=Full-text search， e.g. \"invoice\" or field-scoped like \"from:someone@x.com\". Takes priority over Unread Only / From Address below.,depends_on_key=operation,depends_on_values=list_messages"`

	FromAddress string `json:"from_address" schema:"label=From Address,type=text,help=Only messages sent from this exact address. Ignored if Search is set.,depends_on_key=operation,depends_on_values=list_messages"`

	UnreadOnly bool `json:"unread_only" schema:"label=Unread Only,type=boolean,default=false,depends_on_key=operation,depends_on_values=list_messages"`

	MaxResults float64 `json:"max_results" schema:"label=Max Results,type=number,default=10,depends_on_key=operation,depends_on_values=list_messages"`

	DownloadAttachments bool `json:"download_attachments" schema:"label=Download Attachments,type=boolean,default=true,help=Download attachment files to disk when reading a message.,depends_on_key=operation,depends_on_values=list_messages|get_message"`

	MessageID string `json:"message_id" schema:"label=Message ID,type=text,depends_on_key=operation,depends_on_values=get_message|send_draft|delete_message|create_reply|reply"`

	To string `json:"to" schema:"label=To,type=text,depends_on_key=operation,depends_on_values=send_message|create_draft|create_reply|reply"`

	CC string `json:"cc" schema:"label=Cc,type=text,depends_on_key=operation,depends_on_values=send_message|create_draft|create_reply|reply"`

	BCC string `json:"bcc" schema:"label=Bcc,type=text,depends_on_key=operation,depends_on_values=send_message|create_draft|create_reply|reply"`

	Subject string `json:"subject" schema:"label=Subject,type=text,depends_on_key=operation,depends_on_values=send_message|create_draft"`

	Body string `json:"body" schema:"label=Body,type=textarea,rows=5,help=For create_reply/reply， this is used as the reply comment text.,depends_on_key=operation,depends_on_values=send_message|create_draft|create_reply|reply"`

	BodyType string `json:"body_type" schema:"label=Body Type,type=select,options=text|html,default=text,depends_on_key=operation,depends_on_values=send_message|create_draft"`

	ReplyAll bool `json:"reply_all" schema:"label=Reply All,type=boolean,default=false,help=Reply to everyone on the original message instead of just the sender.,depends_on_key=operation,depends_on_values=create_reply|reply"`
}
