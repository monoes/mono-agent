package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/workflow"
)

// Workflow bundles (spec §5.3): `workflow export --bundle-automations`
// embeds the automation packages the workflow's nodes use, so a shared
// workflow works on import.
//
// Format: the ordinary WorkflowFile JSON plus one optional top-level field,
//
//	"automations": {
//	  "<id>": {"version": "1.2.0", "sha256": "<hex of the .mpkg>", "mpkg": "<base64 .mpkg>"}
//	}
//
// The field is ignored by importers that predate it (the workflow parser
// skips unknown keys), so bundled files stay importable everywhere. A node
// uses automation <id> when its type's prefix resolves to installed package
// <id> on the exporting machine (directly or through a legacy alias); in a
// bundle such node types are written as "<id>.<action>".

// bundledAutomation is one entry of the "automations" field.
type bundledAutomation struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Mpkg    string `json:"mpkg"` // base64 (std encoding) of the .mpkg zip
}

// workflowBundleFile is a WorkflowFile with bundled automations.
type workflowBundleFile struct {
	workflow.WorkflowFile
	Automations map[string]bundledAutomation `json:"automations,omitempty"`
	// Unbundled lists packages the workflow uses that could not be exported
	// (e.g. a legacy package whose sites could not be worked out). The
	// workflow and every exportable package are still written; the
	// importing side reports these as missing, with the reason and hint.
	Unbundled map[string]unbundledAutomation `json:"unbundledAutomations,omitempty"`
	// suggestedUsed: packages exported with their suggested domains
	// (--use-suggested-domains), for the exporter's notice. Not written.
	suggestedUsed map[string][]string
}

// unbundledAutomation says why a used package is not in the bundle.
type unbundledAutomation struct {
	Version string `json:"version,omitempty"`
	Reason  string `json:"reason"`
	Hint    string `json:"hint,omitempty"` // "" when no export option can help
	// LocalOnly: the package opens local addresses (localhost, 127.0.0.1,
	// …), which no exported package may allow; it can only run where it was
	// made, so the recipient has to recreate it.
	LocalOnly bool `json:"localOnly,omitempty"`
}

// bundleOptions controls which site domains an exported package gets.
// Generated legacy packages run unrestricted locally and have no
// site.domains; an exported copy must name them (Registry.Export refuses
// otherwise).
type bundleOptions struct {
	// domains sets site.domains of the exported copy per automation id
	// (--automation-domains <id>=<site,...>).
	domains map[string][]string
	// useSuggested uses a legacy package's suggested domains (derived from
	// its literal navigate URLs) when no explicit domains are given.
	useSuggested bool
}

// parseAutomationDomains parses --automation-domains values "<id>=<a,b>".
func parseAutomationDomains(vals []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, v := range vals {
		id, list, ok := strings.Cut(v, "=")
		id = strings.TrimSpace(id)
		if !ok || id == "" || len(splitCSV(list)) == 0 {
			return nil, errInvalidInput("--automation-domains %q: want <automation id>=<site,...>", v)
		}
		out[id] = append(out[id], splitCSV(list)...)
	}
	return out, nil
}

// bundleWorkflowAutomations exports every installed automation the
// workflow's nodes use into the file's "automations" field. A package that
// cannot be exported is listed under "unbundledAutomations" instead; the
// export as a whole never fails because of one package.
func bundleWorkflowAutomations(file workflow.WorkflowFile, opts bundleOptions) (workflowBundleFile, error) {
	out := workflowBundleFile{WorkflowFile: file}
	reg, err := openAutomationRegistry()
	if err != nil {
		return out, err
	}
	resolve := packageResolver(reg)
	for _, id := range workflowAutomationIDs(file.Nodes, resolve) {
		info, err := reg.Info(id)
		if err != nil {
			continue
		}
		var buf bytes.Buffer
		domains, suggested := exportDomains(reg, id, opts)
		if err := reg.Export(id, &buf, automation.ExportOptions{Domains: domains}); err != nil {
			if out.Unbundled == nil {
				out.Unbundled = map[string]unbundledAutomation{}
			}
			if hosts := localHosts(reg, id); len(hosts) > 0 {
				out.Unbundled[id] = unbundledAutomation{Version: info.Version, LocalOnly: true,
					Reason: fmt.Sprintf("opens a local address (%s) — it can only run on this machine; the recipient has to create it themselves",
						strings.Join(hosts, ", "))}
				continue
			}
			out.Unbundled[id] = unbundledAutomation{Version: info.Version, Reason: err.Error(), Hint: unbundledHint(reg, id)}
			continue
		}
		if suggested {
			if out.suggestedUsed == nil {
				out.suggestedUsed = map[string][]string{}
			}
			out.suggestedUsed[id] = domains
		}
		sum := sha256.Sum256(buf.Bytes())
		if out.Automations == nil {
			out.Automations = map[string]bundledAutomation{}
		}
		out.Automations[id] = bundledAutomation{
			Version: info.Version,
			SHA256:  hex.EncodeToString(sum[:]),
			Mpkg:    base64.StdEncoding.EncodeToString(buf.Bytes()),
		}
	}
	out.Nodes = canonicalNodeTypes(file.Nodes, resolve, out.Automations)
	return out, nil
}

// exportDomains picks site.domains for the exported copy of id: explicit
// ones first, else (opt-in) a legacy package's suggestions — only when it
// opens no local host, which no exported package may allow — else none
// (the package's own). suggested reports that the suggestions were used.
func exportDomains(reg *automation.Registry, id string, opts bundleOptions) (domains []string, suggested bool) {
	if d := opts.domains[id]; len(d) > 0 {
		return d, false
	}
	if opts.useSuggested {
		if p, err := reg.Get(id); err == nil && p.Manifest.Legacy != nil &&
			len(p.Manifest.Legacy.SuggestedDomains) > 0 && len(p.Manifest.Legacy.LocalHosts) == 0 {
			return p.Manifest.Legacy.SuggestedDomains, true
		}
	}
	return nil, false
}

// unbundledHint tells the user how to include id next time.
func unbundledHint(reg *automation.Registry, id string) string {
	if p, err := reg.Get(id); err == nil && p.Manifest.Legacy != nil && len(p.Manifest.Legacy.SuggestedDomains) > 0 {
		return fmt.Sprintf("re-run workflow export with --automation-domains %s=%s (or --use-suggested-domains), or share it separately: monoagentcli automation export %s --domains %s",
			id, strings.Join(p.Manifest.Legacy.SuggestedDomains, ","), id, strings.Join(p.Manifest.Legacy.SuggestedDomains, ","))
	}
	return fmt.Sprintf("re-run workflow export with --automation-domains %s=<site,...> naming the sites its actions open, or share it separately: monoagentcli automation export %s --domains <site,...>", id, id)
}

// suggestedNotices are human lines for packages exported with their
// suggested domains: the recipient's install review will show them.
func suggestedNotices(b workflowBundleFile) []string {
	ids := make([]string, 0, len(b.suggestedUsed))
	for id := range b.suggestedUsed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var lines []string
	for _, id := range ids {
		lines = append(lines, fmt.Sprintf("note: bundled %s with suggested domains %s (the recipient's install review shows them)",
			id, strings.Join(b.suggestedUsed[id], ", ")))
	}
	return lines
}

// unbundledWarnings are human lines for the packages left out of a bundle.
func unbundledWarnings(b workflowBundleFile) []string {
	ids := make([]string, 0, len(b.Unbundled))
	for id := range b.Unbundled {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var lines []string
	for _, id := range ids {
		u := b.Unbundled[id]
		line := fmt.Sprintf("warning: automation %s not bundled: %s", id, u.Reason)
		if u.Hint != "" {
			line += "\n  " + u.Hint
		}
		lines = append(lines, line)
	}
	return lines
}

// localHosts returns the local addresses package id opens: a legacy
// package's recorded local hosts, or local entries of site.domains.
// Exporting cannot fix these (no exported package may allow them).
func localHosts(reg *automation.Registry, id string) []string {
	p, err := reg.Get(id)
	if err != nil {
		return nil
	}
	if p.Manifest.Legacy != nil && len(p.Manifest.Legacy.LocalHosts) > 0 {
		return p.Manifest.Legacy.LocalHosts
	}
	var out []string
	for _, d := range p.Manifest.Site.Domains {
		if isLocalAddress(d) {
			out = append(out, d)
		}
	}
	return out
}

// isLocalAddress reports whether host[:port] names this machine or a
// private network: localhost, *.localhost, *.local, loopback and private
// IP addresses.
func isLocalAddress(hostport string) bool {
	host := strings.ToLower(strings.TrimSpace(hostport))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast())
}
