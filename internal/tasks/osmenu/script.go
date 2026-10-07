package osmenu

import (
	_ "embed"
	"strings"
	"text/template"
)

// osascript runs the menu's two AppleScript lines: the name of the app the
// text was selected in, and the notification. Tests render with a stub.
const osascript = "/usr/bin/osascript"

// funcs escape a template's fields: sh quotes for the shell, plist escapes
// for a property list's text.
var funcs = template.FuncMap{"sh": shellQuote, "plist": plistEscape}

//go:embed templates/action.sh.tmpl
var actionTmpl string

var actionTemplate = template.Must(template.New("action.sh").Funcs(funcs).Parse(actionTmpl))

// scriptView is what the script is rendered from. The template quotes every
// field, and the script only ever expands them inside double quotes.
type scriptView struct {
	CLI, DBPath, ProfileID, Name, Osascript string
}

// renderScript is the Run Shell Script action's script. The selected text
// arrives on its standard input and reaches monoagentcli the same way: it is
// never part of a command line.
func renderScript(v scriptView) (string, error) {
	var b strings.Builder
	if err := actionTemplate.Execute(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}
