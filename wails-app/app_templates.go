// wails-app/app_templates.go
package main

type TemplateInfo struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// GetTemplates lists the message templates (`template list`), or nil when
// there are none or the CLI fails — the frontend reads both as "no
// templates".
func (a *App) GetTemplates() []TemplateInfo {
	var templates []TemplateInfo
	if err := a.cliJSON(profileCLITimeout, &templates, "template", "list"); err != nil || len(templates) == 0 {
		return nil
	}
	return templates
}
