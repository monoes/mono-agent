package service

// GmailNodeSchema documents the config keys GmailNode.Execute reads out of
// its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar. It is never constructed or
// used at runtime; this struct exists solely to generate
// internal/workflow/schemas/service.gmail.json.
//
// The hand-written schemas/service.gmail.json this replaces only exposed 5
// of the 9 operations GmailNode.Execute actually supports ("send_message",
// "list_messages", "get_message", "list_labels", "trash_message") and was
// missing "create_draft", "send_draft", "create_reply", and "reply" —
// along with the "cc", "bcc", "reply_all", and "label_ids" fields those and
// "send_message"/"list_messages" read. Generating from the struct (rather
// than hand-copying the old JSON) surfaces that gap; all fields below are
// read directly by Execute.
//
// credential_platform: gmail
type GmailNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Gmail Account,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=send_message|create_draft|send_draft|create_reply|reply|list_messages|get_message|list_labels|trash_message,default=send_message"`

	To string `json:"to" schema:"label=To,type=text,depends_on_key=operation,depends_on_values=send_message|create_draft"`

	From string `json:"from" schema:"label=From,type=text,depends_on_key=operation,depends_on_values=send_message|create_draft|create_reply|reply"`

	Cc string `json:"cc" schema:"label=Cc,type=text,depends_on_key=operation,depends_on_values=send_message|create_draft"`

	Bcc string `json:"bcc" schema:"label=Bcc,type=text,depends_on_key=operation,depends_on_values=send_message|create_draft"`

	Subject string `json:"subject" schema:"label=Subject,type=text,depends_on_key=operation,depends_on_values=send_message|create_draft"`

	Body string `json:"body" schema:"label=Body,type=textarea,rows=5,depends_on_key=operation,depends_on_values=send_message|create_draft|create_reply|reply"`

	BodyType string `json:"body_type" schema:"label=Body Type,type=select,options=text|html,default=text,depends_on_key=operation,depends_on_values=send_message|create_draft|create_reply|reply"`

	MessageID string `json:"message_id" schema:"label=Message ID,type=text,help=For send_draft this is the draft id returned by create_draft， not the underlying message's own id.,depends_on_key=operation,depends_on_values=send_draft|create_reply|reply|get_message|trash_message"`

	ReplyAll bool `json:"reply_all" schema:"label=Reply All,type=boolean,depends_on_key=operation,depends_on_values=create_reply|reply"`

	Query string `json:"query" schema:"label=Search Query,type=text,placeholder=is:unread from:someone@example.com,depends_on_key=operation,depends_on_values=list_messages"`

	LabelIDs string `json:"label_ids" schema:"label=Label IDs,type=array,item_type=text,depends_on_key=operation,depends_on_values=list_messages"`

	MaxResults float64 `json:"max_results" schema:"label=Max Results,type=number,default=10,depends_on_key=operation,depends_on_values=list_messages"`
}
