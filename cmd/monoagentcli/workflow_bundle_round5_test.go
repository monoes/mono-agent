package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/automation"
)

// N1: a package that opens a local address cannot be bundled by any export
// option. The reason says so, there is no domain hint, and the importing
// side says to recreate it — not to re-import with --yes.
func TestWorkflowBundleLocalOnlyPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeLegacyFile(t, home, "devsite", "get_heading",
		`{"actionType":"get_heading","platform":"devsite","steps":[{"id":"o","type":"navigate","url":"http://localhost:8099/"}]}`)
	reg, err := openAutomationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dev := packageResolver(reg)("devsite", "get_heading")
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "src.db"), JSONOutput: true, ProfileID: "default"}
	id := importWorkflowJSON(t, cfg, `{"name":"dev","nodes":[{"id":"a","type":"devsite.get_heading","name":"A","position":{"x":0,"y":0},"config":{}}],"connections":[]}`)

	var file string
	for _, extra := range [][]string{nil, {"--use-suggested-domains"}, {"--automation-domains", dev + "=localhost:8099"}} {
		f, doc := exportBundle(t, cfg, id, extra...)
		u, ok := doc.Unbundled[dev]
		if !ok || !u.LocalOnly || u.Hint != "" || len(doc.Automations) != 0 {
			t.Fatalf("export %v: unbundled %+v, bundled %v", extra, doc.Unbundled, keysOf(doc.Automations))
		}
		if !strings.Contains(u.Reason, "opens a local address (") || !strings.Contains(u.Reason, "localhost") ||
			!strings.Contains(u.Reason, "the recipient has to create it themselves") || strings.Contains(u.Reason, "--domains") {
			t.Fatalf("reason = %q", u.Reason)
		}
		file = f
	}

	// Importing: JSON lists it without an install command; human output
	// says to recreate it and never suggests --yes.
	t.Setenv("HOME", t.TempDir())
	cfg2 := &globalConfig{DBPath: filepath.Join(t.TempDir(), "dst.db"), JSONOutput: true, ProfileID: "default"}
	var res map[string]any
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg2, "import", "--file", file)), &res); err != nil {
		t.Fatal(err)
	}
	if _, has := res["installCommand"]; has {
		t.Fatalf("an install command was offered for a package that is not in the file: %v", res)
	}
	cfg3 := &globalConfig{DBPath: filepath.Join(t.TempDir(), "dst3.db"), ProfileID: "default"}
	out := runWorkflowSubcmd(t, cfg3, "import", "--file", file)
	if strings.Contains(out, "--yes") || !strings.Contains(out, "recreate "+dev) || !strings.Contains(out, "ask the sender") {
		t.Fatalf("human import output:\n%s", out)
	}
}

func TestIsLocalAddress(t *testing.T) {
	for _, h := range []string{"localhost", "localhost:8099", "127.0.0.1:8099", "[::1]:80", "dev.localhost", "printer.local", "192.168.1.10", "10.0.0.1:3000"} {
		if !isLocalAddress(h) {
			t.Errorf("%q should be local", h)
		}
	}
	for _, h := range []string{"example.com", "*.example.com", "8.8.8.8", "localhost.example.com"} {
		if isLocalAddress(h) {
			t.Errorf("%q should not be local", h)
		}
	}
}

// N2: a bundled package with the installed id and version but other
// content is reported "differs" with the review diff and never installed
// silently; --replace-automations (+ --yes) replaces it after the review.
func TestWorkflowBundleDiffersSameVersion(t *testing.T) {
	// Sender: acme-test 1.0.0 that also allows *.example.com.
	t.Setenv("HOME", t.TempDir())
	dir := writeTestAutomationID(t, "acme-test")
	manifest := filepath.Join(dir, "automation.json")
	b, _ := os.ReadFile(manifest)
	wider := strings.Replace(string(b), `"domains": ["example.com"]`, `"domains": ["example.com", "*.example.com"]`, 1)
	if wider == string(b) {
		t.Fatal("fixture manifest changed; update the test")
	}
	if err := os.WriteFile(manifest, []byte(wider), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := openAutomationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Install(dir, automation.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "src.db"), JSONOutput: true, ProfileID: "default"}
	id := importWorkflowJSON(t, cfg, bundleTestWorkflow)
	file, doc := exportBundle(t, cfg, id)
	if _, ok := doc.Automations["acme-test"]; !ok {
		t.Fatalf("not bundled: %v", doc.Unbundled)
	}

	// Recipient: the plain acme-test 1.0.0 is installed already.
	t.Setenv("HOME", t.TempDir())
	installTestAutomation(t)
	cfg2 := &globalConfig{DBPath: filepath.Join(t.TempDir(), "dst.db"), JSONOutput: true, ProfileID: "default"}
	item := func(args ...string) bundleImportItem {
		t.Helper()
		var res struct {
			Automations []bundleImportItem `json:"automations"`
		}
		if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg2, append([]string{"import", "--file", file}, args...)...)), &res); err != nil {
			t.Fatal(err)
		}
		for _, it := range res.Automations {
			if it.ID == "acme-test" {
				return it
			}
		}
		t.Fatalf("acme-test missing from %+v", res.Automations)
		return bundleImportItem{}
	}
	domains := func() string {
		t.Helper()
		reg, err := openAutomationRegistry()
		if err != nil {
			t.Fatal(err)
		}
		p, err := reg.Get("acme-test")
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(p.Manifest.Site.Domains, ",")
	}

	// --yes alone never replaces.
	it := item("--yes")
	if it.Status != "differs" || it.Changes == nil || strings.Join(it.Changes.AddedDomains, ",") != "*.example.com" ||
		!strings.Contains(it.Error, "--replace-automations") || it.ReviewDetail == nil {
		t.Fatalf("differs item = %+v", it)
	}
	if got := domains(); got != "example.com" {
		t.Fatalf("installed copy changed without --replace-automations: %s", got)
	}
	// --replace-automations without --yes, non-interactive: refused.
	if it := item("--replace-automations"); it.Status != "differs" || !strings.Contains(it.Error, "confirmation required") {
		t.Fatalf("replace without confirmation = %+v", it)
	}
	// Explicit opt-in plus confirmation: replaced, then identical.
	if it := item("--replace-automations", "--yes"); it.Status != "replaced" {
		t.Fatalf("replace = %+v", it)
	}
	if got := domains(); got != "example.com,*.example.com" {
		t.Fatalf("after replace: %s", got)
	}
	if it := item(); it.Status != "present" {
		t.Fatalf("re-import after replace = %+v", it)
	}
}
