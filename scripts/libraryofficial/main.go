// Command libraryofficial writes the official monoes.me library artifacts
// that are not automation packages, and the manifest for all of them. It is
// run by scripts/library-official.sh (make library-official) after the
// script has packed automations/<id> into <out>/<id>.mpkg:
//
//	go run ./scripts/libraryofficial -out <dir>
//
// It copies the bundled workflow templates (workflow-<id>.json) and the
// starter orgs in orgtemplates/ (org-<slug>.json, validated first), then
// writes <out>/manifest.json listing every file with its kind, slug, name,
// description, version, tags, sha256 and size. monoes.me's admin seed
// script uploads the items from that directory as official.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/automations"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// Item is one manifest.json entry.
type Item struct {
	Kind        string   `json:"kind"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Tags        []string `json:"tags"`
	File        string   `json:"file"`
	SHA256      string   `json:"sha256"`
	Size        int64    `json:"size"`
	Visibility  string   `json:"visibility"`
}

// Manifest is manifest.json.
type Manifest struct {
	V     int    `json:"v"`
	Items []Item `json:"items"`
}

// officialAutomations are the packages under automations/ published as
// official.
var officialAutomations = automations.IDs

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "", "output directory (holds the packed <id>.mpkg files)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "libraryofficial: -out is required")
		os.Exit(2)
	}
	m, err := Build(*root, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "libraryofficial:", err)
		os.Exit(1)
	}
	for _, it := range m.Items {
		fmt.Printf("%-10s %-20s %-8s %8d  %s\n", it.Kind, it.Slug, it.Version, it.Size, it.File)
	}
}

// Build writes the workflow and org artifacts into out and the manifest
// covering them and the already packed automation packages.
func Build(root, out string) (*Manifest, error) {
	m := &Manifest{V: 1}
	autos, err := automationItems(root, out)
	if err != nil {
		return nil, err
	}
	wfs, err := workflowItems(root, out)
	if err != nil {
		return nil, err
	}
	orgs, err := orgItems(root, out)
	if err != nil {
		return nil, err
	}
	m.Items = append(append(autos, wfs...), orgs...)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return m, os.WriteFile(filepath.Join(out, "manifest.json"), append(b, '\n'), 0o644)
}

func automationItems(root, out string) ([]Item, error) {
	var items []Item
	for _, id := range officialAutomations {
		var man struct {
			ID, Name, Version, Description, Category string
			Policy                                   struct{ Tier string }
		}
		if err := readJSON(filepath.Join(root, "automations", id, "automation.json"), &man); err != nil {
			return nil, err
		}
		file := id + ".mpkg"
		it := Item{Kind: "automation", Slug: man.ID, Name: man.Name, Description: man.Description,
			Version: man.Version, Tags: tagSet(man.Category, man.Policy.Tier, "web-automation"), File: file, Visibility: "official"}
		if err := hashInto(&it, filepath.Join(out, file)); err != nil {
			return nil, fmt.Errorf("%s: %w (pack it first: monoagentcli automation pack automations/%s -o %s)", id, err, id, file)
		}
		items = append(items, it)
	}
	return items, nil
}

func workflowItems(root, out string) ([]Item, error) {
	paths, err := filepath.Glob(filepath.Join(root, "internal", "workflow", "templates", "*.json"))
	if err != nil || len(paths) == 0 {
		return nil, fmt.Errorf("no workflow templates under internal/workflow/templates (%v)", err)
	}
	sort.Strings(paths)
	var items []Item
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var wf struct {
			Name, Description string
			Nodes             []struct{ Type string }
		}
		if err := json.Unmarshal(raw, &wf); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		id := strings.TrimSuffix(filepath.Base(p), ".json")
		tags := []string{"workflow-template"}
		for _, n := range wf.Nodes {
			if a, _, ok := strings.Cut(n.Type, "."); ok && contains(officialAutomations, a) {
				tags = append(tags, a)
			}
		}
		file := "workflow-" + id + ".json"
		if err := os.WriteFile(filepath.Join(out, file), raw, 0o644); err != nil {
			return nil, err
		}
		it := Item{Kind: "workflow", Slug: id, Name: wf.Name, Description: wf.Description, Version: "1.0.0",
			Tags: tagSet(tags...), File: file, Visibility: "official"}
		if err := hashInto(&it, filepath.Join(out, file)); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// OrgCatalog is orgtemplates/catalog.json: the listing metadata of each
// starter org (the org JSON itself is orgtemplates/<slug>.json).
type OrgCatalog struct {
	V     int `json:"v"`
	Items []struct {
		Slug        string   `json:"slug"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Version     string   `json:"version"`
		Tags        []string `json:"tags"`
	} `json:"items"`
}

// LoadOrgTemplate reads and validates orgtemplates/<slug>.json.
func LoadOrgTemplate(root, slug string) (*orgdesign.Doc, []byte, error) {
	p := filepath.Join(root, "orgtemplates", slug+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, err
	}
	var d orgdesign.Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", p, err)
	}
	if d.Name != slug {
		return nil, nil, fmt.Errorf("%s: name %q, want %q", p, d.Name, slug)
	}
	if err := orgdesign.Validate(&d); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", p, err)
	}
	return &d, raw, nil
}

func orgItems(root, out string) ([]Item, error) {
	var cat OrgCatalog
	if err := readJSON(filepath.Join(root, "orgtemplates", "catalog.json"), &cat); err != nil {
		return nil, err
	}
	var items []Item
	for _, c := range cat.Items {
		_, raw, err := LoadOrgTemplate(root, c.Slug)
		if err != nil {
			return nil, err
		}
		file := "org-" + c.Slug + ".json"
		if err := os.WriteFile(filepath.Join(out, file), raw, 0o644); err != nil {
			return nil, err
		}
		it := Item{Kind: "org", Slug: c.Slug, Name: c.Name, Description: c.Description, Version: c.Version,
			Tags: tagSet(c.Tags...), File: file, Visibility: "official"}
		if err := hashInto(&it, filepath.Join(out, file)); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}

func hashInto(it *Item, p string) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	it.SHA256, it.Size = hex.EncodeToString(sum[:]), int64(len(b))
	return nil
}

// tagSet drops empty and repeated tags, keeping the first order.
func tagSet(tags ...string) []string {
	out := []string{}
	for _, t := range tags {
		if t != "" && !contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
