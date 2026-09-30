package orggrant

import (
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/workflow"
)

// OutboundNodes lists the nodes of wf that may act on the outside world or
// can't be shown not to. A grant to a workflow with any of them defaults to
// approval "required" and tier irreversible (C-34), so a person approves
// its calls even at autonomy mid.
//
// The classifier is deny-by-default: only the node types in readOnlyNodes
// (and the config-dependent reads in readOnlyByConfig) are not outbound.
// Every other type counts, including unknown and future ones and every
// installed automation package's actions. A false positive costs one extra
// decision, a false negative an unreviewed side effect.
func OutboundNodes(wf *workflow.Workflow) []string {
	if wf == nil {
		return nil
	}
	var out []string
	for _, n := range wf.Nodes {
		if n.Disabled {
			continue
		}
		if isOutboundNode(n) {
			label := n.Type
			if n.Name != "" {
				label = n.Name + " (" + n.Type + ")"
			}
			out = append(out, label)
		}
	}
	sort.Strings(out)
	return out
}

func isOutboundNode(n workflow.WorkflowNode) bool {
	t := n.Type
	if strings.HasPrefix(t, "trigger.") || readOnlyNodes[t] {
		return false
	}
	if read, ok := readOnlyByConfig[t]; ok {
		return !read(n.Config)
	}
	return true
}

// readOnlyNodes are the node types reviewed as reading or shaping data
// without acting on the outside world. Each entry was checked against its
// implementation (and, for browser automations, its action JSON: no step
// marked sideEffect). trigger.* nodes are pass-through in the engine and
// never looked up in the registry. Keep this list exact: no prefix or
// suffix rules, since installed packages choose their own action names.
var readOnlyNodes = setOf(
	// Control flow and data shaping. core.code runs goja with only $input
	// and $json bound; core.human_in_loop waits for a person.
	"core.aggregate", "core.code", "core.compare_datasets", "core.filter",
	"core.human_in_loop", "core.if", "core.limit", "core.merge",
	"core.remove_duplicates", "core.set", "core.sort", "core.split_in_batches",
	"core.stop_error", "core.switch", "core.wait",
	"data.compression", "data.crypto", "data.datetime", "data.html",
	"data.markdown", "data.xml",
	// Image transforms write a new derived file; image.info only reads.
	// remove_background downloads its model with a GET.
	"image.adjust", "image.convert", "image.crop", "image.info",
	"image.remove_background", "image.resize", "image.thumbnail",
	"image.vault_get", "vault.secret_get",
	// Reads over the network.
	"comm.email_read", "comm.outlook_read", "system.rss_read",
	"ai.read_page", // static GET, SSRF-guarded, no browser
	// AI with no tools: ai.choose is a TypeSafe Jev classification. The
	// other ai.* are deprecated stubs that fail without doing anything
	// (ai.agent is left out regardless).
	"ai.choose",
	"ai.chat", "ai.classify", "ai.embed", "ai.extract", "ai.transform",
	// Bookkeeping in mono-agent's own stores: nothing leaves the machine
	// and nothing is overwritten beyond the rows the node owns.
	// discovery.search_jobs reads public job boards with GETs.
	"people.save", "people.sync_outlook_message",
	"applications.create", "applications.list",
	"applications.prepare", "applications.set_status", "applications.tag",
	"discovery.search_jobs", "documents.render", "image.vault_save",
	// Official browser automations' read actions (automations/*/actions):
	// navigate and extract only.
	"hackernews.get_post_metrics", "hackernews.list_comments",
	"instagram.export_followers", "instagram.extract_post_data",
	"instagram.find_by_keyword", "instagram.list_post_comments",
	"instagram.list_user_posts", "instagram.scrape_profile_info",
	"linkedin.export_followers", "linkedin.find_by_keyword",
	"linkedin.list_post_comments", "linkedin.list_user_posts",
	"linkedin.scrape_profile_info",
	"producthunt.get_launch_metrics", "producthunt.list_comments",
	"tiktok.export_followers", "tiktok.find_by_keyword",
	"tiktok.list_user_videos", "tiktok.list_video_comments",
	"tiktok.scrape_profile_info",
	"x.export_followers", "x.find_by_keyword", "x.scrape_profile_info",
	// Legacy unprefixed names (noderegistry.Build's aliases) of the above.
	"aggregate", "code", "filter", "if", "limit", "merge", "set", "sort",
	"switch", "wait", "compression", "crypto", "datetime", "html",
	"markdown", "xml", "email_read", "rss_read",
)

// readOnlyByConfig are node types that read or write depending on their
// config; the function reports whether this config only reads.
var readOnlyByConfig = map[string]func(map[string]interface{}) bool{
	"http.request":     httpReadOnly,
	"http_request":     httpReadOnly,
	"data.spreadsheet": spreadsheetReadOnly,
	"spreadsheet":      spreadsheetReadOnly,
}

// httpReadOnly: GET (the node's default) and HEAD. A templated method is
// not known until run time, so it counts as a write.
func httpReadOnly(cfg map[string]interface{}) bool {
	method, _ := cfg["method"].(string)
	return method == "" || strings.EqualFold(method, "GET") || strings.EqualFold(method, "HEAD")
}

func spreadsheetReadOnly(cfg map[string]interface{}) bool {
	op, _ := cfg["operation"].(string)
	return op == "read_csv" || op == "read_xlsx"
}

// outboundNodes and outboundPrefixes name the registered node types
// reviewed as outbound. isOutboundNode doesn't need them (everything off
// the read-only lists is outbound); they exist so the registry test can
// tell a reviewed outbound type from one nobody classified.
var outboundNodes = setOf(
	"agent.ask", "ai.agent", // agents that can run tools
	// Local agent turns in the workspace-write sandbox: nothing in the
	// node keeps the runtime from using its tools.
	"ai.extract_page", "applications.evaluate",
	"browser.jev", // goal-driven agent in the user's browser
	"http.ftp", "http.ssh", "system.execute_command",
	"data.write_binary_file", // writes any local path
	"vault.secret_save",      // overwrites credentials
)

var outboundPrefixes = []string{
	"comm.", "service.", "action.", "db.", "org.",
	// Browser automations; their read actions are in readOnlyNodes.
	"gemini.", "hackernews.", "instagram.", "linkedin.", "producthunt.",
	"tiktok.", "x.",
}

func classifiedOutbound(t string) bool {
	if outboundNodes[t] {
		return true
	}
	for _, p := range outboundPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func setOf(types ...string) map[string]bool {
	m := make(map[string]bool, len(types))
	for _, t := range types {
		m[t] = true
	}
	return m
}

// GrantTier is the decision tier a call to a granted automation gets when
// it needs a decision (plan §7.7): irreversible with outbound nodes,
// consequential otherwise.
func GrantTier(outbound []string) string {
	if len(outbound) > 0 {
		return orgdesign.TierIrreversible
	}
	return orgdesign.TierConsequential
}
