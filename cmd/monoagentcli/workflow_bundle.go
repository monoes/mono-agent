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
// uses automation <id> when its type is "<id>.<action>" and <id> is an
// installed automation on the exporting machine.

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
	for _, id := range workflowAutomationIDs(file.Nodes) {
		info, err := reg.Info(id)
		if err != nil {
			continue // "<x>.<y>" that is not an installed automation (core.set, trigger.manual, …)
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
	return out, nil
}

// workflowAutomationIDs returns the sorted, distinct "<id>" prefixes of
// node types of the form "<id>.<action>".
func workflowAutomationIDs(nodes []workflow.WorkflowFileNode) []string {
	seen := map[string]bool{}
	var ids []string
	for _, n := range nodes {
		id, _, ok := strings.Cut(n.Type, ".")
		if !ok || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// bundleImportItem reports what `workflow import` did (or would do) with
// one bundled automation.
type bundleImportItem struct {
	ID               string `json:"id"`
	Version          string `json:"version"`
	Status           string `json:"status"` // present | conflict | missing | installed | failed
	InstalledVersion string `json:"installedVersion,omitempty"`
	Error            string `json:"error,omitempty"`
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
	data, err := base64.StdEncoding.DecodeString(b.Mpkg)
	if err != nil {
		return fmt.Errorf("decode bundled package: %w", err)
	}
	want := strings.ToLower(b.SHA256)
	if want == "" {
		return fmt.Errorf("bundled package %s has no sha256", id)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("bundled package %s: sha256 mismatch", id)
	}
	dir, err := os.MkdirTemp("", "workflow-bundle-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "bundle.mpkg")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	pkg, err := automation.OpenFile(path)
	if err != nil {
		return fmt.Errorf("open bundled package %s: %w", id, err)
	}
	if pkg.Manifest.ID != id {
		return fmt.Errorf("bundled package under %q declares id %q; not installed", id, pkg.Manifest.ID)
	}
	opts := automation.InstallOptions{Source: automation.SourceImported, ExpectSHA256: want}
	dry := opts
	dry.DryRun = true
	review, err := reg.Install(path, dry)
	if err != nil {
		return err
	}
	out := o.out
	if out == nil {
		out = io.Discard
	}
	fmt.Fprintln(out, bundleReviewLine(review))
	if review.ID != id {
		return fmt.Errorf("bundled package under %q reviews as %q; not installed", id, review.ID)
	}
	if issuesHaveErrors(review.Issues) {
		return fmt.Errorf("bundled package %s has validation errors; not installed", id)
	}
	if !o.yes && !confirmYes(o.in, out, fmt.Sprintf("Install %s %s?", review.ID, review.Version)) {
		return errors.New("install declined")
	}
	res, err := reg.Install(path, opts)
	if err != nil {
		return err
	}
	if res.ID != id {
		return fmt.Errorf("bundled package under %q installed as %q", id, res.ID)
	}
	return nil
}

// bundleReviewLine summarises an install review on one line: id, version,
// publisher, domains and capabilities.
func bundleReviewLine(r *automation.InstallResult) string {
	rv := r.Review
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
	return fmt.Sprintf("Bundled automation %s %s — publisher %s — domains %s — %s",
		r.ID, r.Version, orDash(rv.Publisher), orDash(strings.Join(rv.Domains, ",")), strings.Join(caps, "; "))
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
