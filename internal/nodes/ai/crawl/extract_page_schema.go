package crawl

// ExtractPageNodeSchema documents the config keys ExtractPageNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
type ExtractPageNodeSchema struct {
	URL string `json:"url" schema:"label=URL,type=text,required,placeholder=https://shop.com/product/123"`

	ExtractMode string `json:"extract_mode" schema:"label=Extraction Mode,type=select,required,default=natural,options=natural|css,help=natural = describe what to extract in plain English. css = provide CSS selectors."`

	Prompt string `json:"prompt" schema:"label=What to Extract,type=textarea,rows=3,placeholder=Extract product name， price， rating， and all reviews,depends_on_key=extract_mode,depends_on_values=natural,help=Describe what you want to extract in plain English."`

	Fields string `json:"fields" schema:"label=CSS Selectors (JSON),type=code,language=json,rows=5,placeholder={\"name\": \"h1.title\"， \"price\": \".price-tag\"},depends_on_key=extract_mode,depends_on_values=css,help=JSON map of field name to CSS selector. Use @attr for attributes: img.hero@src"`

	ListSelector string `json:"list_selector" schema:"label=List Selector,type=text,placeholder=.product-card,help=If set， extracts an array of items matching this selector."`

	RenderMode string `json:"render_mode" schema:"label=Render Mode,type=select,default=auto,options=auto|static|browser"`

	WaitSelector string `json:"wait_selector" schema:"label=Wait For Selector,type=text,depends_on_key=render_mode,depends_on_values=browser"`
}
