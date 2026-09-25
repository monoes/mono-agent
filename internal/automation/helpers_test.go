package automation

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// acmeFiles is a small valid imported package exercising fragments,
// selectors and a page script.
func acmeFiles() map[string]string {
	return map[string]string{
		"automation.json": `{
  "schema": "monoagent.automation/v1",
  "id": "acme-crm",
  "name": "Acme CRM",
  "version": "1.2.0",
  "publisher": {"name": "Jane"},
  "engine": ">=0.1.0",
  "icon": "icon.svg",
  "site": {"startUrl": "https://app.acme.com/", "domains": ["app.acme.com", "*.acme.com"]},
  "permissions": {"steps": ["navigate", "click", "type", "call_fragment", "call_action", "page_script", "extract_*"], "scripts": ["parse.js"], "downloads": false},
  "actions": ["list_deals", "create_contact"],
  "policy": {"tier": "standard"}
}
`,
		"actions/list_deals.json": `{"actionType":"list_deals","automation":"acme-crm","sideEffects":"read","steps":[
  {"id":"open","type":"navigate","url":"https://app.acme.com/deals"},
  {"id":"banner","type":"call_fragment","fragment":"dismiss"},
  {"id":"row","type":"click","configKey":"deal.row"},
  {"id":"parse","type":"page_script","script":"parse.js"}
]}
`,
		"actions/create_contact.json": `{"actionType":"create_contact","automation":"acme-crm","sideEffects":"write","steps":[
  {"id":"open","type":"navigate","url":"https://app.acme.com/contacts/new"},
  {"id":"email","type":"type","configKey":"contact.email","value":"{{email}}"},
  {"id":"save","type":"click","configKey":"contact.save","sideEffect":true}
]}
`,
		"fragments/dismiss.json": `{"name":"dismiss","steps":[{"id":"x","type":"click","configKey":"banner.close"}]}
`,
		"selectors.json": `{
  "banner.close": {"candidates": [{"css": ".banner .close"}]},
  "contact.email": {"candidates": [{"css": "input[name=email]"}, {"aria": {"role": "textbox", "name": "Email"}}]},
  "contact.save": {"candidates": [{"text": "Save"}]},
  "deal.row": {"candidates": [{"xpath": "//tr[1]"}]}
}
`,
		"scripts/parse.js":             "return [...document.querySelectorAll('tr')].map(r => r.innerText);\n",
		"icon.svg":                     "<svg/>\n",
		"README.md":                    "# Acme\n",
		"tests/list_deals.expect.json": "[]\n",
		"forms/create_contact.json":    "{}\n",
		"recordings/rec-1/meta.json":   "{}\n",
	}
}

func writeTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func acmeDir(t *testing.T) string {
	return writeTree(t, filepath.Join(t.TempDir(), "acme-crm"), acmeFiles())
}

func newReg(t *testing.T) *Registry {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), ".monoagent"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func packDir(t *testing.T, dir string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Pack(dir, &buf); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	out := filepath.Join(t.TempDir(), "pkg.mpkg")
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

func errorsOnly(is []IssueJSON) []IssueJSON {
	var out []IssueJSON
	for _, i := range is {
		if i.Severity == "error" {
			out = append(out, i)
		}
	}
	return out
}
