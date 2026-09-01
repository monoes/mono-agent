package service

// DiscordNodeSchema documents the config keys DiscordNode.Execute reads out
// of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar. It is never constructed or
// used at runtime; this struct exists solely to generate
// internal/workflow/schemas/service.discord.json.
//
// credential_platform: discord
type DiscordNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Discord Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=send_message|list_messages|add_reaction|list_channels,default=send_message"`

	ChannelID string `json:"channel_id" schema:"label=Channel ID,type=text,depends_on_key=operation,depends_on_values=send_message|list_messages|add_reaction"`

	Text string `json:"text" schema:"label=Message,type=textarea,rows=3,depends_on_key=operation,depends_on_values=send_message"`

	Limit float64 `json:"limit" schema:"label=Limit,type=number,default=50,depends_on_key=operation,depends_on_values=list_messages"`

	MessageID string `json:"message_id" schema:"label=Message ID,type=text,depends_on_key=operation,depends_on_values=add_reaction"`

	Emoji string `json:"emoji" schema:"label=Emoji,type=text,placeholder=e.g. 👍 or unicode name,depends_on_key=operation,depends_on_values=add_reaction"`

	GuildID string `json:"guild_id" schema:"label=Server (Guild) ID,type=text,depends_on_key=operation,depends_on_values=list_channels"`
}
