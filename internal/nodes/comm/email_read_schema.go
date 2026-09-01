package comm

// EmailReadNodeSchema documents the config keys EmailReadNode.Execute reads
// out of its map[string]interface{} config, via `schema:"..."` tags
// consumed by internal/tools/schemagen (see that package's doc comment for
// the tag grammar). It is never constructed or used at runtime.
//
// EmailReadNode.Execute currently always returns an error (the go-imap
// dependency isn't installed) — see the node's own doc comment. Its config
// contract is still documented there, and that's what this struct
// generates from, matching how the hand-written schema it replaces already
// exposed these fields ahead of the dependency landing.
//
// "tls" has no entry in the hand-written schemas/comm.email_read.json this
// replaces, even though EmailReadNode's own doc comment has documented a
// "tls" (bool, default true) config key since before this conversion.
// Generating from the doc-comment contract (rather than hand-copying the
// old JSON) surfaces that gap.
//
// credential_platform: imap
type EmailReadNodeSchema struct {
	IMAPHost string `json:"imap_host" schema:"label=IMAP Host,type=text,required,placeholder=imap.gmail.com"`

	IMAPPort float64 `json:"imap_port" schema:"label=IMAP Port,type=number,required,default=993"`

	Username string `json:"username" schema:"label=Username,type=text,required"`

	Password string `json:"password" schema:"label=Password / App Password,type=password,required"`

	TLS bool `json:"tls" schema:"label=Use TLS,type=boolean,default=true"`

	Mailbox string `json:"mailbox" schema:"label=Mailbox,type=text,default=INBOX"`

	Limit float64 `json:"limit" schema:"label=Max Emails,type=number,default=20"`

	UnreadOnly bool `json:"unread_only" schema:"label=Unread Only,type=boolean,default=false"`
}
