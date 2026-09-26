package nodes

import (
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// declaredOutputs collects an action's outputs.success values after a run:
// each name is a variable (e.g. extract_text's variable_name) or, failing
// that, a step id whose result data is used. Only declarative packages (no
// requires.native) get this: native built-ins keep their outputs exactly as
// they were. Returns nil when nothing resolved.
func declaredOutputs(ex *action.ActionExecutor, automationID, actionType string) map[string]interface{} {
	pkg := ex.Package()
	if pkg == nil || nativeOf(pkg) != "" {
		return nil
	}
	def, err := action.GetLoader().Load(automationID, actionType)
	if err != nil || def == nil {
		return nil
	}
	out := map[string]interface{}{}
	for _, name := range def.Outputs["success"] {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		v := ex.Resolver().ResolvePath(name)
		if v == nil {
			if sr := ex.ExecContext().GetStepResult(name); sr != nil && sr.Data != nil {
				v = sr.Data
			}
		}
		if v != nil {
			out[name] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// nativeOf is the package's requires.native ("" for declarative packages).
func nativeOf(pkg action.PackageContext) string {
	if n, ok := pkg.(interface{ Native() string }); ok {
		return n.Native()
	}
	if m, ok := bootedManifest(pkg.ID()); ok {
		return m.Requires.Native
	}
	return ""
}

// withDeclaredOutputs merges declared outputs over a copy of the input item.
func withDeclaredOutputs(inputJSON, declared map[string]interface{}) map[string]interface{} {
	item := make(map[string]interface{}, len(inputJSON)+len(declared))
	for k, v := range inputJSON {
		item[k] = v
	}
	for k, v := range declared {
		item[k] = v
	}
	return item
}
