package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	Hint    string `json:"hint"`
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
		lines = append(lines, fmt.Sprintf("warning: automation %s not bundled: %s\n  %s", id, u.Reason, u.Hint))
	}
	return lines
}

// bundleImportItem reports what `workflow import` did (or would do) with
// one bundled automation.
type bundleImportItem struct {
	ID               string `json:"id"`
	Version          string `json:"version"`
	Status           string `json:"status"` // present | conflict | missing | installed | failed
	InstalledVersion string `json:"installedVersion,omitempty"`
	Error            string `json:"error,omitempty"`
	// NotBundled: the exporter could not include this package (see Error);
	// --yes cannot install it.
	NotBundled bool `json:"notBundled,omitempty"`
	// Review and ReviewDetail describe a "missing" package (a dry-run review
	// of the pinned bytes, nothing installed), so a GUI can show what it
	// would install before asking.
	Review       string              `json:"review,omitempty"`
	ReviewDetail *bundleReviewDetail `json:"reviewDetail,omitempty"`
}

// bundleReviewDetail is the structured form of bundleReviewLine.
type bundleReviewDetail struct {
	ID           string               `json:"id"`
	Version      string               `json:"version"`
	Publisher    string               `json:"publisher,omitempty"`
	Domains      []string             `json:"domains"`
	Capabilities []string             `json:"capabilities"`
	Replaces     *automation.Replaced `json:"replaces,omitempty"`
}

// bundleImportOptions controls handleBundledAutomations.
type bundleImportOptions struct {
	yes         bool // install missing packages without asking
	interactive bool // a person can answer a prompt on in
	in          io.Reader
	out         io.Writer // prompts and reviews
}

// handleBundledAutomations reads the "automations" field of raw workflow
// JSON. Bundled packages that are already installed are "present"; the
// others are installed with --yes (or after a prompt), and otherwise
// reported as "missing". Returns nil when the file bundles nothing.
// Failures are reported per package and never fail the workflow import.
func handleBundledAutomations(raw []byte, o bundleImportOptions) []bundleImportItem {
	var doc struct {
		Automations map[string]bundledAutomation   `json:"automations"`
		Unbundled   map[string]unbundledAutomation `json:"unbundledAutomations"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Automations)+len(doc.Unbundled) == 0 {
		return nil
	}
	ids := make([]string, 0, len(doc.Automations))
	for id := range doc.Automations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := make([]bundleImportItem, 0, len(ids))
	reg, err := openAutomationRegistry()
	if err != nil {
		for _, id := range ids {
			items = append(items, bundleImportItem{ID: id, Version: doc.Automations[id].Version, Status: "failed", Error: err.Error()})
		}
		return items
	}
	// Every id in the index, removed built-ins included (Info hides those).
	all, err := reg.List(true)
	if err != nil {
		for _, id := range ids {
			items = append(items, bundleImportItem{ID: id, Version: doc.Automations[id].Version, Status: "failed", Error: err.Error()})
		}
		return items
	}
	known := make(map[string]automation.InstalledInfo, len(all))
	for _, in := range all {
		known[in.ID] = in
	}
	for _, id := range ids {
		b := doc.Automations[id]
		item := bundleImportItem{ID: id, Version: b.Version, Status: "missing"}
		// Never install over an installed id, including an uninstalled
		// built-in (its index entry stays, marked removed).
		if info, ok := known[id]; ok {
			item.Status, item.InstalledVersion = "present", info.Version
			if info.Removed {
				item.Status, item.Error = "conflict", "a removed built-in has this id; restore it with `automation restore`"
			}
			items = append(items, item)
			continue
		}
		if o.yes || o.interactive {
			if err := installBundledAutomation(reg, id, b, o); err != nil {
				item.Status, item.Error = "failed", err.Error()
			} else {
				item.Status = "installed"
			}
		} else if review, err := reviewBundledAutomation(reg, id, b); err != nil {
			item.Error = err.Error() // still "missing": --yes would fail the same way
		} else {
			item.Review, item.ReviewDetail = bundleReviewLine(review), newBundleReviewDetail(review)
		}
		items = append(items, item)
	}
	return append(items, unbundledItems(doc.Unbundled, doc.Automations, known)...)
}

// unbundledItems reports packages the exporter could not bundle: present
// when installed here, otherwise missing with the exporter's reason and hint.
func unbundledItems(unbundled map[string]unbundledAutomation, bundled map[string]bundledAutomation,
	known map[string]automation.InstalledInfo) []bundleImportItem {
	ids := make([]string, 0, len(unbundled))
	for id := range unbundled {
		if _, dup := bundled[id]; !dup {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var items []bundleImportItem
	for _, id := range ids {
		u := unbundled[id]
		item := bundleImportItem{ID: id, Version: u.Version, Status: "missing", NotBundled: true,
			Error: "not in the bundle: " + u.Reason + "; " + u.Hint}
		if info, ok := known[id]; ok && !info.Removed {
			item.Status, item.InstalledVersion, item.Error = "present", info.Version, ""
		}
		items = append(items, item)
	}
	return items
}

// installBundledAutomation installs one bundled package that is not
// installed yet. The bundle is untrusted input: the sha256 is required and
// pinned through to the install, the package's manifest id must equal its
// bundle key, and a one-line review summary is always printed (also with
// --yes) before anything is written.
func installBundledAutomation(reg *automation.Registry, id string, b bundledAutomation, o bundleImportOptions) error {
	path, cleanup, err := stageBundledAutomation(id, b)
	if err != nil {
		return err
	}
	defer cleanup()
	review, err := reviewStagedBundle(reg, id, b, path)
	if err != nil {
		return err
	}
	out := o.out
	if out == nil {
		out = io.Discard
	}
	fmt.Fprintln(out, bundleReviewLine(review))
	if !o.yes && !confirmYes(o.in, out, fmt.Sprintf("Install %s %s?", review.ID, review.Version)) {
		return errors.New("install declined")
	}
	res, err := reg.Install(path, automation.InstallOptions{Source: automation.SourceImported, ExpectSHA256: strings.ToLower(b.SHA256)})
	if err != nil {
		return err
	}
	if res.ID != id {
		return fmt.Errorf("bundled package under %q installed as %q", id, res.ID)
	}
	return nil
}

// reviewBundledAutomation is the dry-run half of installBundledAutomation:
// the same sha256 pinning and id checks, and nothing is installed.
func reviewBundledAutomation(reg *automation.Registry, id string, b bundledAutomation) (*automation.InstallResult, error) {
	path, cleanup, err := stageBundledAutomation(id, b)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return reviewStagedBundle(reg, id, b, path)
}

// stageBundledAutomation decodes one bundled package, checks it against its
// pinned sha256 and writes it to a private temp file. The caller runs
// cleanup.
func stageBundledAutomation(id string, b bundledAutomation) (string, func(), error) {
	data, err := base64.StdEncoding.DecodeString(b.Mpkg)
	if err != nil {
		return "", nil, fmt.Errorf("decode bundled package: %w", err)
	}
	want := strings.ToLower(b.SHA256)
	if want == "" {
		return "", nil, fmt.Errorf("bundled package %s has no sha256", id)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != want {
		return "", nil, fmt.Errorf("bundled package %s: sha256 mismatch", id)
	}
	dir, err := os.MkdirTemp("", "workflow-bundle-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	path := filepath.Join(dir, "bundle.mpkg")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

// reviewStagedBundle opens a staged bundle, checks its manifest id and runs
// the install dry run (pinned to the bundle's sha256).
func reviewStagedBundle(reg *automation.Registry, id string, b bundledAutomation, path string) (*automation.InstallResult, error) {
	pkg, err := automation.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open bundled package %s: %w", id, err)
	}
	if pkg.Manifest.ID != id {
		return nil, fmt.Errorf("bundled package under %q declares id %q; not installed", id, pkg.Manifest.ID)
	}
	review, err := reg.Install(path, automation.InstallOptions{Source: automation.SourceImported, ExpectSHA256: strings.ToLower(b.SHA256), DryRun: true})
	if err != nil {
		return nil, err
	}
	if review.ID != id {
		return nil, fmt.Errorf("bundled package under %q reviews as %q; not installed", id, review.ID)
	}
	if issuesHaveErrors(review.Issues) {
		return nil, fmt.Errorf("bundled package %s has validation errors; not installed", id)
	}
	return review, nil
}

// newBundleReviewDetail is bundleReviewLine as data.
func newBundleReviewDetail(r *automation.InstallResult) *bundleReviewDetail {
	rv := r.Review
	// The registry's plain-language capabilities, then the short technical
	// list the review line prints (steps, scripts, downloads, tier).
	caps := append(append([]string{}, rv.Capabilities...), bundleCapabilities(rv)...)
	return &bundleReviewDetail{
		ID: r.ID, Version: r.Version, Publisher: rv.Publisher,
		Domains: append([]string{}, rv.Domains...), Capabilities: caps,
		Replaces: rv.Replaces,
	}
}

// bundleReviewLine summarises an install review on one line: id, version,
// publisher, domains and capabilities.
func bundleReviewLine(r *automation.InstallResult) string {
	rv := r.Review
	return fmt.Sprintf("Bundled automation %s %s — publisher %s — domains %s — %s",
		r.ID, r.Version, orDash(rv.Publisher), orDash(strings.Join(rv.Domains, ",")), strings.Join(bundleCapabilities(rv), "; "))
}

// bundleCapabilities is the short capability list of the review line.
func bundleCapabilities(rv automation.Review) []string {
	caps := []string{"steps: " + orDash(strings.Join(rv.Steps, ","))}
	if len(rv.Steps) == 0 {
		caps[0] = "steps: unrestricted"
	}
	if len(rv.Scripts) > 0 {
		caps = append(caps, "scripts: "+strings.Join(rv.Scripts, ","))
	}
	if rv.Downloads {
		caps = append(caps, "downloads")
	}
	if rv.Tier != "" {
		caps = append(caps, "tier: "+rv.Tier)
	}
	if rv.PolicyBlocked {
		caps = append(caps, "policy-blocked")
	}
	return caps
}

// printBundleImport prints the human summary of handleBundledAutomations.
func printBundleImport(out io.Writer, items []bundleImportItem) {
	missing := 0
	for _, it := range items {
		if it.NotBundled && it.Status == "missing" {
			fmt.Fprintf(out, "Automation %s %s: missing (%s)\n", it.ID, it.Version, it.Error)
			continue
		}
		switch it.Status {
		case "present":
			fmt.Fprintf(out, "Automation %s: already installed (%s; bundle has %s)\n", it.ID, it.InstalledVersion, it.Version)
		case "installed":
			fmt.Fprintf(out, "Automation %s %s: installed from the bundle\n", it.ID, it.Version)
		case "failed", "conflict":
			fmt.Fprintf(out, "Automation %s %s: not installed: %s\n", it.ID, it.Version, it.Error)
		default:
			missing++
			fmt.Fprintf(out, "Automation %s %s: missing (bundled)\n", it.ID, it.Version)
		}
	}
	if missing > 0 {
		fmt.Fprintln(out, "Pass --yes to `workflow import` to install bundled automations.")
	}
}
