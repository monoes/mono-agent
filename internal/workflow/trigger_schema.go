package workflow

// ScheduleTriggerSchema and WebhookTriggerSchema document the config keys
// read by activateSchedule and activateWebhook in trigger_manager.go — see
// internal/nodes/control.SetNodeSchema's doc comment (in that package) for
// why these are companion structs rather than the runtime config, and
// internal/tools/schemagen for the tag grammar. Unlike every other
// generated node type, trigger.schedule and trigger.webhook config isn't
// read inside a NodeExecutor.Execute method — it's read directly out of
// WorkflowNode.Config by the trigger manager when the trigger is activated.
//
// trigger.manual is intentionally not converted: it has no config at all
// (see schemas/trigger.manual.json — an empty fields array), so a companion
// struct would have zero tagged fields and add nothing over the existing
// hand-written file.
//
// "hmac_secret" has no entry in the hand-written schemas/trigger.webhook.json
// this replaces — activateWebhook has read node.Config["hmac_secret"] since
// it was written, but the schema never exposed a way to set it from the UI.
// Separately, the hand-written schema marks "path" as optional ("Leave
// blank to auto-generate"), but activateWebhook actually rejects an empty
// path with an error — there is no auto-generation path in this codebase.
// Generating from the struct fixes both gaps.
type ScheduleTriggerSchema struct {
	Cron string `json:"cron" schema:"label=Cron Expression,type=text,required,placeholder=e.g. 0 0 9 * * 1-5,help=6-field cron: seconds minutes hours day month weekday. e.g. '0 0 9 * * 1-5' = weekdays at 9am."`

	Timezone string `json:"timezone" schema:"label=Timezone,type=text,default=UTC,help=IANA timezone name， e.g. America/New_York"`
}

type WebhookTriggerSchema struct {
	Path string `json:"path" schema:"label=Webhook Path,type=text,required,placeholder=/webhook/my-hook,help=URL path suffix. Required — activation fails if left blank."`

	Method string `json:"method" schema:"label=HTTP Method,type=select,options=GET|POST|PUT|PATCH|DELETE,default=POST"`

	HMACSecret string `json:"hmac_secret" schema:"label=HMAC Secret,type=password,help=If set， incoming requests must include a valid HMAC signature computed with this secret."`

	AuthHeader string `json:"auth_header" schema:"label=Auth Header Name,type=text,placeholder=X-Webhook-Secret,help=If set， requests must include this header with the auth token value."`

	AuthToken string `json:"auth_token" schema:"label=Auth Token,type=password,help=Secret value expected in the auth header."`
}
