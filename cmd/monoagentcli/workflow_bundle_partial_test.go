package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLegacyFile writes ~/.monoagent/actions/<platform>/<name>.json.
func writeLegacyFile(t *testing.T, home, platform, name, body string) {
	t.Helper()
	dir := filepath.Join(home, ".monoagent", "actions", platform)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// importWorkflowJSON imports wf (JSON) and returns the new workflow id.
func importWorkflowJSON(t *testing.T, cfg *globalConfig, wf string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "wf.json")
	if err := os.WriteFile(src, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	var imported struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "import", "--file", src)), &imported); err != nil {
		t.Fatal(err)
	}
	return imported.ID
}

// exportBundle runs `workflow export <id> --bundle-automations -o F` and
// parses F.
func exportBundle(t *testing.T, cfg *globalConfig, id string) (string, workflowBundleFile) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "bundled.json")
	runWorkflowSubcmd(t, cfg, "export", id, "--bundle-automations", "-o", out)
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc workflowBundleFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("bundle is not valid JSON (%d bytes): %v", len(raw), err)
	}
	return out, doc
}

// D5(a): one unexportable package (a legacy package whose sites cannot be
// worked out) no longer sinks the whole export. The workflow and every
// exportable package are written; the unexportable one is listed with its
// reason and hint, and the importing side reports it as missing.
func TestWorkflowBundlePartialExport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeLegacyFile(t, home, "nosite", "get_heading",
		`{"actionType":"get_heading","platform":"nosite","steps":[{"id":"h","type":"find_element","selector":"h1"}]}`)
	writeLegacyFile(t, home, "examplesite", "get_heading",
		`{"actionType":"get_heading","platform":"examplesite","steps":[
  {"id":"open","type":"navigate","url":"https://example.com/"},
  {"id":"h","type":"find_element","selector":"h1"}]}`)
	reg, err := openAutomationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	resolve := packageResolver(reg)
	noSite, exSite := resolve("nosite", "get_heading"), resolve("examplesite", "get_heading")
	if noSite == "" || exSite == "" {
		t.Fatalf("legacy packages not resolved: %q %q", noSite, exSite)
	}

	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "src.db"), JSONOutput: true, ProfileID: "default"}
	id := importWorkflowJSON(t, cfg, `{"name":"partial","nodes":[
  {"id":"a","type":"nosite.get_heading","name":"A","position":{"x":0,"y":0},"config":{}},
  {"id":"b","type":"examplesite.get_heading","name":"B","position":{"x":200,"y":0},"config":{}}
],"connections":[{"id":"a-b","source":"a","target":"b"}]}`)
	file, doc := exportBundle(t, cfg, id)
	if len(doc.Nodes) != 2 || len(doc.Connections) != 1 {
		t.Fatalf("workflow missing from the bundle: %d nodes, %d connections", len(doc.Nodes), len(doc.Connections))
	}
	if _, ok := doc.Automations[exSite]; !ok || len(doc.Automations) != 1 {
		t.Fatalf("bundled %v, want exactly %s", keysOf(doc.Automations), exSite)
	}
	u, ok := doc.Unbundled[noSite]
	if !ok || u.Reason == "" || !strings.Contains(u.Hint, "automation export "+noSite+" --domains") {
		t.Fatalf("unbundled entry for %s = %+v (all: %v)", noSite, u, doc.Unbundled)
	}
	// The unbundled node keeps its original type; the bundled one names its package.
	if doc.Nodes[0].Type != "nosite.get_heading" || doc.Nodes[1].Type != exSite+".get_heading" {
		t.Fatalf("node types: %q, %q", doc.Nodes[0].Type, doc.Nodes[1].Type)
	}

	// Fresh machine: the workflow imports, the bundled package installs, the
	// unbundled one is reported missing with the reason and hint.
	t.Setenv("HOME", t.TempDir())
	cfg2 := &globalConfig{DBPath: filepath.Join(t.TempDir(), "dst.db"), JSONOutput: true, ProfileID: "default"}
	var res struct {
		ID          string             `json:"id"`
		Automations []bundleImportItem `json:"automations"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg2, "import", "--file", file, "--yes")), &res); err != nil {
		t.Fatal(err)
	}
	if res.ID == "" {
		t.Fatal("workflow not imported")
	}
	got := map[string]bundleImportItem{}
	for _, it := range res.Automations {
		got[it.ID] = it
	}
	if got[exSite].Status != "installed" {
		t.Fatalf("%s: %+v", exSite, got[exSite])
	}
	if it := got[noSite]; it.Status != "missing" || !it.NotBundled || !strings.Contains(it.Error, "--domains") {
		t.Fatalf("%s: %+v", noSite, it)
	}
}

// D5(b): a legacy action on a built-in platform lives in the legacy package
// (local-hackernews), not the built-in. It must be bundled from there, under
// that package's node type, while built-in actions keep theirs.
func TestWorkflowBundleLegacyActionOnBuiltinPlatform(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeLegacyFile(t, home, "hackernews", "custom_scrape",
		`{"actionType":"custom_scrape","platform":"hackernews","steps":[
  {"id":"open","type":"navigate","url":"https://news.ycombinator.com/"},
  {"id":"h","type":"find_element","selector":"a"}]}`)
	reg, err := openAutomationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	resolve := packageResolver(reg)
	legacy := resolve("hackernews", "custom_scrape")
	if legacy == "" || legacy == "hackernews" {
		t.Fatalf("hackernews.custom_scrape resolved to %q, want the legacy package", legacy)
	}
	if got := resolve("hackernews", "list_comments"); got != "hackernews" {
		t.Fatalf("built-in action resolved to %q", got)
	}
	if got := resolve("hackernews", "LIST_COMMENTS"); got != "hackernews" {
		t.Fatalf("action match should ignore case, got %q", got)
	}

	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "src.db"), JSONOutput: true, ProfileID: "default"}
	id := importWorkflowJSON(t, cfg, `{"name":"hn","nodes":[
  {"id":"a","type":"hackernews.custom_scrape","name":"A","position":{"x":0,"y":0},"config":{}},
  {"id":"b","type":"hackernews.list_comments","name":"B","position":{"x":200,"y":0},"config":{}}
],"connections":[]}`)
	_, doc := exportBundle(t, cfg, id)
	if _, ok := doc.Automations[legacy]; !ok {
		t.Fatalf("legacy package %s not bundled: %v (unbundled %v)", legacy, keysOf(doc.Automations), doc.Unbundled)
	}
	if doc.Nodes[0].Type != legacy+".custom_scrape" || doc.Nodes[1].Type != "hackernews.list_comments" {
		t.Fatalf("node types: %q, %q", doc.Nodes[0].Type, doc.Nodes[1].Type)
	}
}

// A failed bundle export leaves no file behind (and never an empty one).
func TestWorkflowExportMissingWorkflowWritesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "x.db"), JSONOutput: true, ProfileID: "default"}
	out := filepath.Join(t.TempDir(), "bundled.json")
	captureStdout(t, func() {
		cmd := newWorkflowCmd(cfg)
		cmd.SetArgs([]string{"export", "no-such-id", "--bundle-automations", "-o", out})
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		if err := cmd.Execute(); err == nil {
			t.Error("export of an unknown workflow should fail")
		}
	})
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a failed export left a file behind")
	}
}
