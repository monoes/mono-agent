// cmd/monoagentcli/profile_documents_test.go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newProfileDocsCLITestDB(t *testing.T) string {
	t.Helper()
	return newApplicationCLITestDB(t) // reuses the shared migration-seeding helper from application_test.go
}

func setFakeMonomindOnPathCLI(t *testing.T) {
	t.Helper()
	fakeDir := t.TempDir()
	src, err := filepath.Abs("../../internal/monomind/testdata/fake-monomind.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(fakeDir, "monomind")); err != nil {
		t.Fatalf("symlink fake monomind onto PATH: %v", err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func runProfileCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newProfileCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestProfileUploadListDeleteDocument(t *testing.T) {
	setFakeMonomindOnPathCLI(t)
	dbPath := newProfileDocsCLITestDB(t)

	docPath := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(docPath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	uploadOut, err := runProfileCmd(t, dbPath, "upload-document", docPath)
	if err != nil {
		t.Fatalf("upload-document: %v (%s)", err, uploadOut)
	}
	if !strings.Contains(uploadOut, `"id"`) {
		t.Fatalf("expected JSON id in output, got: %s", uploadOut)
	}

	listOut, err := runProfileCmd(t, dbPath, "documents", "list")
	if err != nil {
		t.Fatalf("documents list: %v", err)
	}
	if !strings.Contains(listOut, "resume.txt") {
		t.Fatalf("expected filename in list output, got: %s", listOut)
	}
}

func TestProfileUploadDocumentRecordsIndexedStatus(t *testing.T) {
	setFakeMonomindOnPathCLI(t)
	dbPath := newProfileDocsCLITestDB(t)

	docPath := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(docPath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	uploadOut, err := runProfileCmd(t, dbPath, "upload-document", docPath)
	if err != nil {
		t.Fatalf("upload-document: %v (%s)", err, uploadOut)
	}
	if !strings.Contains(uploadOut, `"indexed": true`) {
		t.Fatalf("expected indexed:true in output, got: %s", uploadOut)
	}

	listOut, err := runProfileCmd(t, dbPath, "documents", "list")
	if err != nil {
		t.Fatalf("documents list: %v", err)
	}
	if !strings.Contains(listOut, `"Indexed": true`) {
		t.Fatalf("expected Indexed:true in list output, got: %s", listOut)
	}
}

func TestProfileUploadDocumentRecordsIndexingFailure(t *testing.T) {
	setFakeMonomindOnPathCLI(t)
	t.Setenv("INGEST_FAIL", "1")
	dbPath := newProfileDocsCLITestDB(t)

	docPath := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(docPath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	uploadOut, err := runProfileCmd(t, dbPath, "upload-document", docPath)
	if err != nil {
		t.Fatalf("upload-document should still succeed even when indexing fails: %v (%s)", err, uploadOut)
	}
	if !strings.Contains(uploadOut, `"indexed": false`) {
		t.Fatalf("expected indexed:false in output, got: %s", uploadOut)
	}
	if !strings.Contains(uploadOut, `"index_error"`) {
		t.Fatalf("expected index_error in output, got: %s", uploadOut)
	}

	listOut, err := runProfileCmd(t, dbPath, "documents", "list")
	if err != nil {
		t.Fatalf("documents list: %v", err)
	}
	if !strings.Contains(listOut, `"Indexed": false`) {
		t.Fatalf("expected Indexed:false in list output, got: %s", listOut)
	}
	if !strings.Contains(listOut, `"IndexError"`) {
		t.Fatalf("expected IndexError in list output, got: %s", listOut)
	}
}

func TestProfileSearchKnowledge(t *testing.T) {
	setFakeMonomindOnPathCLI(t)
	dbPath := newProfileDocsCLITestDB(t)

	out, err := runProfileCmd(t, dbPath, "search-knowledge", "backend engineer")
	if err != nil {
		t.Fatalf("search-knowledge: %v (%s)", err, out)
	}
	if !strings.Contains(out, "distributed systems") {
		t.Fatalf("expected excerpt text in output, got: %s", out)
	}
}
