package comm

// TelegramNodeSchema documents the config keys TelegramNode.Execute reads
// out of its map[string]interface{} config, via `schema:"..."` tags
// consumed by internal/tools/schemagen (see that package's doc comment for
// the tag grammar). It is never constructed or used at runtime.
//
// Like comm.twilio, this node has no credential_id/credential_picker field:
// bot_token is entered directly. The hand-written schema this replaces
// still declared "credential_platform": "telegram" at the top level even
// without a credential_id field — that top-level value is independent of
// per-field types (see NodeSchema.CredentialPlatform in schema_loader.go),
// and is preserved here via the doc-comment line below. Note this is a
// different "telegram" than the browserPlatforms entry of the same name in
// internal/workflow/execution.go, which is for a separate browser-session
// telegram.* node family, not this Bot-API-based comm.telegram node.
//
// "message" (not the doc comment's "text") is the field key, matching
// TelegramNode.telegramText's preferred config key and the hand-written
// schema this replaces.
//
// credential_platform: telegram
type TelegramNodeSchema struct {
	BotToken string `json:"bot_token" schema:"label=Bot Token,type=password,required,help=Get from @BotFather on Telegram."`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=send_message|send_photo|get_updates,default=send_message"`

	ChatID string `json:"chat_id" schema:"label=Chat ID,type=text,help=Numeric chat ID or @channel_username.,depends_on_key=operation,depends_on_values=send_message|send_photo"`

	Message string `json:"message" schema:"label=Message,type=textarea,rows=3,depends_on_key=operation,depends_on_values=send_message|send_photo"`

	PhotoURL string `json:"photo_url" schema:"label=Photo URL / File Path,type=text,depends_on_key=operation,depends_on_values=send_photo"`

	ParseMode string `json:"parse_mode" schema:"label=Parse Mode,type=select,options=plain|Markdown|HTML,default=plain"`
}
