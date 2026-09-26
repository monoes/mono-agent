//go:build !windows

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The document, capture viewer, node palette and org reload bindings shell
// out to `monoagentcli` and never read the database themselves.
func TestDocumentPaletteAndReloadBindingsShellOut(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
case "$*" in
  *"documents get"*) echo '{"id":"doc-1","filename":"cv.pdf","path":"/v/doc-1.pdf","size_bytes":7,"source":"upload","application_id":"","created_at":"c","indexed":true,"index_error":"","stale":false,"url":"","capture_dir":"","summary_status":""}' ;;
  *"documents capture"*) echo '{"title":"T","url":"https://x.test/","captured_at":"c","word_count":3,"readable":"# Hi","screenshot":"","summary":"","transcript":"","summary_status":{"status":"done","runtime":"claude","error":"","finished_at":"f"}}' ;;
  *"node palette"*) echo '{"control":[{"type":"core.if","label":"If","category":"control","description":"Branch"}],"triggers":[]}' ;;
  *"org reload"*) echo '{"v":1,"org":"growth","reloaded":true}' ;;
esac
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")

	doc, err := a.GetProfileDocument("doc-1")
	if err != nil || doc.ID != "doc-1" || doc.Path != "/v/doc-1.pdf" || doc.SizeBytes != 7 || !doc.Indexed {
		t.Fatalf("GetProfileDocument = %+v, %v", doc, err)
	}
	if p, err := a.documentPath("doc-1"); err != nil || p != "/v/doc-1.pdf" {
		t.Fatalf("documentPath = %q, %v", p, err)
	}
	view, err := a.GetCaptureView("cap-1")
	if err != nil || view.Title != "T" || view.WordCount != 3 || view.SummaryStatus == nil || view.SummaryStatus.Runtime != "claude" {
		t.Fatalf("GetCaptureView = %+v, %v", view, err)
	}
	palette := a.GetWorkflowNodeTypes()
	control, _ := palette["control"].([]interface{})
	if len(control) != 1 || control[0].(map[string]interface{})["type"] != "core.if" {
		t.Fatalf("GetWorkflowNodeTypes = %+v", palette)
	}
	if got := a.ReloadOrg("growth"); !strings.Contains(got, `"reloaded":true`) {
		t.Fatalf("ReloadOrg = %q", got)
	}

	want := []string{
		"--profile work --json profile documents get doc-1",
		"--profile work --json profile documents get doc-1",
		"--profile work --json profile documents capture cap-1",
		"--profile work --json node palette",
		"--profile work --json org reload growth",
	}
	if got := loggedArgs(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CLI calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A missing document is nil plus an error, as before.
func TestGetProfileDocumentNotFound(t *testing.T) {
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, "echo 'document doc-999 not found' >&2; exit 2\n"))
	a := newTestApp(t)
	a.ctx = context.Background()
	if doc, err := a.GetProfileDocument("doc-999"); err == nil || doc != nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("GetProfileDocument = %+v, %v", doc, err)
	}
	if _, err := a.GetCaptureView("doc-999"); err == nil {
		t.Fatal("GetCaptureView hid the CLI failure")
	}
}
