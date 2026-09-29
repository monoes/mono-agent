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

// topLevelRef matches a reference to a top-level field of the item a node
// receives: `$json.path`, `$json["path"]`, `.json.path` (the dot form must
// start the reference, so `$node["X"].json.path` is not one). Granted runs
// lift the input's fields to the top level (LiftInput), so a node the
// trigger feeds reads the role's arguments this way.
var topLevelRef = regexp.MustCompile(`(?:\$json|(?:^|[\s(|{])\.json)(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\])`)

// templateBlock matches one `{{ ... }}` expression.
var templateBlock = regexp.MustCompile(`(?s)\{\{.*?\}\}`)

// InputFields lists the fields of the granted run's input that wf's enabled
// nodes reference in templates, sorted and without duplicates: every
// `input.<field>` reference, and the top-level `$json.<field>` references
// of the nodes the trigger item reaches directly (a later node's $json is
// the previous node's output, not the arguments). Reserved trigger keys
// (IsReservedTriggerKey) are never listed.
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
	entry := triggerFedNodes(wf)
	seen := map[string]bool{}
	for _, n := range wf.Nodes {
		if n.Disabled {
			continue
		}
		collectInputRefs(n.Config, inputRef, seen)
		if entry[n.ID] {
			collectInputRefs(n.Config, topLevelRef, seen)
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		if !IsReservedTriggerKey(k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// triggerFedNodes returns the ids of the non-trigger nodes whose input is
// the trigger item: those with no incoming connection, and the successors
// of trigger nodes (through chained triggers, which pass items through).
func triggerFedNodes(wf *workflow.Workflow) map[string]bool {
	byID := make(map[string]workflow.WorkflowNode, len(wf.Nodes))
	for _, n := range wf.Nodes {
		byID[n.ID] = n
	}
	hasIncoming := map[string]bool{}
	out := map[string][]string{}
	for _, c := range wf.Connections {
		hasIncoming[c.TargetNodeID] = true
		out[c.SourceNodeID] = append(out[c.SourceNodeID], c.TargetNodeID)
	}
	isTrigger := func(n workflow.WorkflowNode) bool { return strings.HasPrefix(n.Type, "trigger.") }
	fed := map[string]bool{}
	visited := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		for _, next := range out[id] {
			n, ok := byID[next]
			if !ok {
				continue
			}
			if isTrigger(n) {
				walk(next)
			} else {
				fed[next] = true
			}
		}
	}
	for _, n := range wf.Nodes {
		switch {
		case isTrigger(n):
			walk(n.ID)
		case !hasIncoming[n.ID]:
			fed[n.ID] = true
		}
	}
	return fed
}

func collectInputRefs(v interface{}, ref *regexp.Regexp, seen map[string]bool) {
	switch x := v.(type) {
	case string:
		if !strings.Contains(x, "{{") {
			return
		}
		for _, block := range templateBlock.FindAllString(x, -1) {
			for _, m := range ref.FindAllStringSubmatch(block, -1) {
				if m[1] != "" {
					seen[m[1]] = true
				} else if m[2] != "" {
					seen[m[2]] = true
				}
			}
		}
	case map[string]interface{}:
		for _, e := range x {
			collectInputRefs(e, ref, seen)
		}
	case []interface{}:
		for _, e := range x {
			collectInputRefs(e, ref, seen)
		}
	case []string:
		for _, e := range x {
			collectInputRefs(e, ref, seen)
		}
	}
}
