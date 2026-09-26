//go:build !windows

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The image-vault bindings go through `monoagentcli image …`, not the
// database.
func TestImageVaultBindingsShellOut(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	row := `{"id":"img-002","seq":2,"path":"/v/img-002.png","filename":"img-002.png","size_bytes":9,"source":"upload","workflow_id":"","execution_id":"","label":"Logo","created_at":"2026-09-26T11:00:00Z","url":"/vault-image/img-002.png"}`
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
case "$*" in
  *" image list "*) echo '[`+row+`]' ;;
  *" image search "*) echo '[]' ;;
  *" image get "*|*" image add "*) echo '`+row+`' ;;
  *" image data "*) echo '{"id":"img-002","mime_type":"image/png","size_bytes":3,"data_url":"data:image/png;base64,YWJj"}' ;;
  *" image stats"*) echo '{"count":2,"total_bytes":19}' ;;
  *" image export "*) echo '{"id":"img-002","path":"/out/logo.png"}' ;;
  *) echo '{}' ;;
esac
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")
	orig := saveImageDialog
	saveImageDialog = func(*App, string) (string, error) { return "/out/logo.png", nil }
	t.Cleanup(func() { saveImageDialog = orig })

	if rows, err := a.GetVaultImages(0); err != nil || len(rows) != 1 || rows[0]["url"] != "/vault-image/img-002.png" || rows[0]["size_bytes"] != float64(9) {
		t.Fatalf("GetVaultImages = %v, %v", rows, err)
	}
	if rows, err := a.SearchVaultImages("-logo"); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("SearchVaultImages = %#v, %v", rows, err)
	}
	if im, err := a.GetVaultImage("img-002"); err != nil || im["label"] != "Logo" {
		t.Fatalf("GetVaultImage = %v, %v", im, err)
	}
	if d, err := a.GetVaultImageData("img-002"); err != nil || d != "data:image/png;base64,YWJj" {
		t.Fatalf("GetVaultImageData = %q, %v", d, err)
	}
	if im, err := a.AddVaultImage("/pics/a.png", "Logo"); err != nil || im["id"] != "img-002" {
		t.Fatalf("AddVaultImage = %v, %v", im, err)
	}
	if _, err := a.AddVaultImage("/pics/b.png", ""); err != nil {
		t.Fatal(err)
	}
	if got := a.SaveVaultImageToFile("img-002", "logo.png"); got != "/out/logo.png" {
		t.Fatalf("SaveVaultImageToFile = %q", got)
	}
	if err := a.UpdateVaultImageLabel("img-002", "New"); err != nil {
		t.Fatal(err)
	}
	if err := a.UpdateVaultImageLabel("img-002", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteVaultImage("img-002"); err != nil {
		t.Fatal(err)
	}
	if st, err := a.GetVaultStats(); err != nil || st["count"] != float64(2) || st["total_bytes"] != float64(19) {
		t.Fatalf("GetVaultStats = %v, %v", st, err)
	}
	want := []string{
		"--profile work --json image list --limit 200",
		"--profile work --json image search -- -logo",
		"--profile work --json image get -- img-002",
		"--profile work --json image data -- img-002",
		"--profile work --json image add --label Logo -- /pics/a.png",
		"--profile work --json image add -- /pics/b.png",
		"--profile work --json image get -- img-002",
		"--profile work --json image export -- img-002 /out/logo.png",
		"--profile work --json image label -- img-002 New",
		"--profile work --json image label -- img-002",
		"--profile work --json image delete -- img-002",
		"--profile work --json image stats",
	}
	if got := loggedArgs(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("argv:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A CLI failure (e.g. exit 2 for an unknown id) reaches the page as the
// CLI's stderr; SaveVaultImageToFile keeps its "error: …" string contract
// and never opens the dialog for an unknown image.
func TestImageVaultBindingsReportCLIErrors(t *testing.T) {
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo 'vault image not found: "nope"' >&2; exit 2
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	opened := false
	orig := saveImageDialog
	saveImageDialog = func(*App, string) (string, error) { opened = true; return "/x", nil }
	t.Cleanup(func() { saveImageDialog = orig })

	if _, err := a.GetVaultImage("nope"); err == nil || err.Error() != `vault image not found: "nope"` {
		t.Fatalf("GetVaultImage err = %v", err)
	}
	if _, err := a.GetVaultImageData("nope"); err == nil {
		t.Fatal("GetVaultImageData: want error")
	}
	if err := a.DeleteVaultImage("nope"); err == nil {
		t.Fatal("DeleteVaultImage: want error")
	}
	if rows, err := a.GetVaultImages(10); err == nil || rows != nil {
		t.Fatalf("GetVaultImages = %v, %v", rows, err)
	}
	if got := a.SaveVaultImageToFile("nope", "x.png"); got != "error: image not found" || opened {
		t.Fatalf("SaveVaultImageToFile = %q, dialog opened %v", got, opened)
	}
}
