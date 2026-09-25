package browserjev

// NodeSchema documents the config keys Node.Execute reads — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct, and internal/tools/schemagen for the tag grammar.
type NodeSchema struct {
	URL string `json:"url" schema:"label=Start URL,type=text,required,help=Opened in a new tab of your connected browser."`

	Goal string `json:"goal" schema:"label=Goal,type=textarea,required,rows=4,help=What to achieve in plain language — include every value to enter. The run stops at DONE or BLOCKED."`

	APIKey string `json:"api_key" schema:"label=TypeSafe API Key,type=password,default=@secret:typesafe,help=Falls back to TYPESAFE_API_KEY."`

	Model string `json:"model" schema:"label=Jev Model,type=text,default=jev-latest"`

	TextRuntime string `json:"text_runtime" schema:"label=Text Runtime,type=text,default=claude,help=Local agent that writes TYPE_TEXT values (monoagentcli agent scan --installed)."`

	TextModel string `json:"text_model" schema:"label=Text Model,type=text,help=Model for the text runtime； a small one (e.g. haiku) keeps runs fast."`

	MaxActions float64 `json:"max_actions" schema:"label=Max Actions,type=number,default=40,help=Browser actions per run (decisions are capped at twice this)."`

	Timeout float64 `json:"timeout" schema:"label=Timeout (seconds),type=number,default=300"`

	FailOnBlocked bool `json:"fail_on_blocked" schema:"label=Fail When Not Done,type=boolean,help=Error the node unless the run ends DONE (otherwise status is on the item)."`
}
