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
}

// bundleWorkflowAutomations exports every installed automation the
// workflow's nodes use into the file's "automations" field.
func bundleWorkflowAutomations(file workflow.WorkflowFile) (workflowBundleFile, error) {
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
		if err := reg.Export(id, &buf, automation.ExportOptions{}); err != nil {
			return out, fmt.Errorf("bundle automation %s: %w", id, err)
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

// workflowAutomationIDs returns the sorted, distinct package ids used by
// node types of the form "<prefix>.<action>". resolve maps a prefix to the
// installed package id ("" when the prefix is not an automation, e.g.
// core.set or trigger.manual); it goes through the definition source, so an
// aliased prefix (foo → local-foo) bundles the package that actually runs.
func workflowAutomationIDs(nodes []workflow.WorkflowFileNode, resolve func(prefix string) string) []string {
	seen := map[string]bool{}
	var ids []string
	for _, n := range nodes {
		prefix, _, ok := strings.Cut(n.Type, ".")
		if !ok || prefix == "" {
			continue
		}
		id := resolve(prefix)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// packageResolver resolves a node-type prefix the way the action loader
// does (the registry's DefSource, which also maps a legacy platform alias
// such as "google_maps" to its generated local-* package), then falls back
// to installed packages the DefSource hides (disabled): an exact id, or a
// generated legacy package whose recorded platform is the prefix.
func packageResolver(reg *automation.Registry) func(string) string {
	src := reg.DefSource()
	var infos []automation.InstalledInfo
	listed := false
	return func(prefix string) string {
		if src != nil {
			if pc := src.Package(prefix); pc != nil {
				return pc.ID()
			}
		}
		if info, err := reg.Info(prefix); err == nil && !info.Removed {
			return info.ID
		}
		if !listed {
			infos, _ = reg.List(false)
			listed = true
		}
		for _, info := range infos {
			if info.LegacyPlatform != "" && strings.EqualFold(info.LegacyPlatform, prefix) {
				return info.ID
			}
		}
		return ""
	}
}

// canonicalNodeTypes rewrites "<alias>.<action>" node types to
// "<package id>.<action>" wherever the prefix resolved to a different
// (bundled) package id. A legacy alias is a local convenience of the
// exporting machine — an imported package never claims one — so a bundled
// workflow must name the package it ships.
func canonicalNodeTypes(nodes []workflow.WorkflowFileNode, resolve func(string) string, bundled map[string]bundledAutomation) []workflow.WorkflowFileNode {
	out := make([]workflow.WorkflowFileNode, len(nodes))
	copy(out, nodes)
	for i, n := range out {
		prefix, act, ok := strings.Cut(n.Type, ".")
		if !ok || prefix == "" {
			continue
		}
		if id := resolve(prefix); id != "" && id != prefix {
			if _, isBundled := bundled[id]; isBundled {
				out[i].Type = id + "." + act
			}
		}
	}
	return out
}

// bundleImportItem reports what `workflow import` did (or would do) with
// one bundled automation.
type bundleImportItem struct {
	ID               string `json:"id"`
	Version          string `json:"version"`
	Status           string `json:"status"` // present | conflict | missing | installed | failed
	InstalledVersion string `json:"installedVersion,omitempty"`
	Error            string `json:"error,omitempty"`
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
		Automations map[string]bundledAutomation `json:"automations"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Automations) == 0 {
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
