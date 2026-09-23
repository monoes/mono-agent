package orggrant

import (
	"regexp"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/workflow"
)

// inputRef matches a reference to a field of the granted run's input inside
// a template: `input.path`, `input["path"]`, `input['path']` (as in
// `{{ $json.input.path }}`).
var inputRef = regexp.MustCompile(`\binput(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[\s*["']([^"'\]]+)["']\s*\])`)

// templateBlock matches one `{{ ... }}` expression.
var templateBlock = regexp.MustCompile(`(?s)\{\{.*?\}\}`)

// InputFields lists the fields of the granted run's input that wf's enabled
// nodes reference in templates, sorted and without duplicates.
//
// A grant with no declared input_schema advertises these as the tool's
// properties. Without them the tool's schema has no properties at all, and
// monomind turns an MCP tool's inputSchema into a zod object of its
// properties, which strips every key it does not list: the role's
// arguments reach the workflow as `input: {}` on every runtime. Listing the
// fields the workflow reads is what lets them through.
func InputFields(wf *workflow.Workflow) []string {
	if wf == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, n := range wf.Nodes {
		if n.Disabled {
			continue
		}
		collectInputRefs(n.Config, seen)
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func collectInputRefs(v interface{}, seen map[string]bool) {
	switch x := v.(type) {
	case string:
		if !strings.Contains(x, "{{") {
			return
		}
		for _, block := range templateBlock.FindAllString(x, -1) {
			for _, m := range inputRef.FindAllStringSubmatch(block, -1) {
				if m[1] != "" {
					seen[m[1]] = true
				} else if m[2] != "" {
					seen[m[2]] = true
				}
			}
		}
	case map[string]interface{}:
		for _, e := range x {
			collectInputRefs(e, seen)
		}
	case []interface{}:
		for _, e := range x {
			collectInputRefs(e, seen)
		}
	case []string:
		for _, e := range x {
			collectInputRefs(e, seen)
		}
	}
}
