package crawl

// ReadPageNodeSchema documents the config keys ReadPageNode.Execute reads
// out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
type ReadPageNodeSchema struct {
	URL string `json:"url" schema:"label=URL,type=text,required,placeholder=https://example.com/article"`

	RenderMode string `json:"render_mode" schema:"label=Render Mode,type=select,default=auto,options=auto|static|browser,help=auto = try static first， fall back to browser if page needs JS."`

	IncludeLinks bool `json:"include_links" schema:"label=Include Links,type=boolean,default=true"`

	IncludeImages bool `json:"include_images" schema:"label=Include Images,type=boolean,default=true"`

	IncludeTables bool `json:"include_tables" schema:"label=Include Tables,type=boolean,default=true"`

	KeepNav bool `json:"keep_nav" schema:"label=Keep Nav/Header/Footer,type=boolean,default=false,help=Keep navigation， header， and footer content (usually boilerplate)."`

	MaxTokens float64 `json:"max_tokens" schema:"label=Max Tokens,type=number,default=0,help=Truncate markdown to ~N tokens. 0 = no limit."`

	WaitSelector string `json:"wait_selector" schema:"label=Wait For Selector,type=text,placeholder=#main-content,depends_on_key=render_mode,depends_on_values=browser,help=CSS selector to wait for before extracting (browser mode only)."`
}
