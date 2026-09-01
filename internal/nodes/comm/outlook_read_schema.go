package comm

// OutlookReadNodeSchema documents the config keys OutlookReadNode.Execute
// reads out of its map[string]interface{} config, via `schema:"..."` tags
// consumed by internal/tools/schemagen (see that package's doc comment for
// the tag grammar). It is never constructed or used at runtime.
//
// Like the hand-written schemas/comm.outlook_read.json this replaces, the
// field key is "app_password" (not "password"): OutlookReadNode.Execute
// reads config["password"] first and falls back to config["app_password"],
// so either key works at runtime, but the schema keeps the historical key
// name to avoid a gratuitous rename of what the UI writes.
//
// Unlike comm.email_read, this node has no "tls" config — the IMAP dialer
// always connects to outlook.office365.com:993 over TLS, matching both the
// hand-written schema and OutlookReadNode.Execute's hardcoded imapHost/
// imapPort constants.
//
// credential_platform: outlook
type OutlookReadNodeSchema struct {
	Email string `json:"email" schema:"label=Outlook Email Address,type=text,required,placeholder=you@outlook.com"`

	AppPassword string `json:"app_password" schema:"label=App Password,type=password,required"`

	Mailbox string `json:"mailbox" schema:"label=Mailbox,type=text,default=INBOX"`

	Limit float64 `json:"limit" schema:"label=Max Emails,type=number,default=10"`

	UnreadOnly bool `json:"unread_only" schema:"label=Unread Only,type=boolean,default=false"`
}
