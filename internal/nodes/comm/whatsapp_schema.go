package comm

// WhatsAppNodeSchema documents the config keys WhatsAppNode.Execute reads
// out of its map[string]interface{} config — see TwilioNodeSchema's doc
// comment for why this node has no credential_id/credential_picker field
// (access_token/phone_number_id are entered directly, matching the
// hand-written schema this replaces), and internal/tools/schemagen for the
// tag grammar.
//
// credential_platform: whatsapp
type WhatsAppNodeSchema struct {
	PhoneNumberID string `json:"phone_number_id" schema:"label=Phone Number ID,type=text,required"`

	AccessToken string `json:"access_token" schema:"label=Access Token,type=password,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=send_message|send_template|send_media,default=send_message"`

	To string `json:"to" schema:"label=Recipient Number,type=text,required,placeholder=+15551234567"`

	Text string `json:"text" schema:"label=Message Text,type=textarea,rows=3,depends_on_key=operation,depends_on_values=send_message"`

	TemplateName string `json:"template_name" schema:"label=Template Name,type=text,depends_on_key=operation,depends_on_values=send_template"`

	TemplateLanguage string `json:"template_language" schema:"label=Template Language,type=text,default=en_US,depends_on_key=operation,depends_on_values=send_template"`

	MediaURL string `json:"media_url" schema:"label=Media URL,type=text,depends_on_key=operation,depends_on_values=send_media"`
}
