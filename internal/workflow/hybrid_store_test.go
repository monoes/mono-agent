package workflow

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestHybridWorkflowStore_DeleteWorkflow_SurfacesFileCleanupError guards the
// DeleteWorkflow fix used as the compensating action after a failed atomic
// import (see createOrOverwriteWorkflowAtomically in
// cmd/monoagentcli/workflow.go): before this fix, DeleteWorkflow discarded
// every error from both halves unconditionally, so a caller using it as
// cleanup was told "success" even when a half genuinely failed to delete —
// exactly the scenario here, where the file half can't be removed (its
// directory is read-only) but the SQL half succeeds.
func TestHybridWorkflowStore_DeleteWorkflow_SurfacesFileCleanupError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory write-permission removal does not block file deletion the same way on Windows")
	}
	dir := t.TempDir()
	files, err := NewWorkflowFileStore(dir)
	if err != nil {
		t.Fatalf("NewWorkflowFileStore: %v", err)
	}
	sqlStore, rawDB := newMigratedStore(t)
	store := NewHybridWorkflowStore(files, sqlStore)
	ctx := context.Background()

	wf := &Workflow{ID: "wf-cleanup", Name: "wf-cleanup"}
	if err := store.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	path := filepath.Join(dir, "wf-cleanup.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected workflow file to exist at %s: %v", path, err)
	}

	// Remove write+execute from the directory so os.Remove (called by
	// WorkflowFileStore.DeleteWorkflow) fails with permission denied,
	// regardless of the file's own permissions — deletion is governed by
	// the containing directory's permissions on POSIX, not the file's.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // let t.TempDir() clean up

	err = store.DeleteWorkflow(ctx, "wf-cleanup")
	if err == nil {
		t.Fatal("expected DeleteWorkflow to surface the file store's permission error, got nil")
	}
	if !strings.Contains(err.Error(), "file store") {
		t.Fatalf("expected the error to identify the file store half, got: %v", err)
	}

	// The file must still be there — deletion genuinely failed, not just
	// reported as failed.
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("expected the leftover file to still exist after the failed delete: %v", statErr)
	}

	// The SQL half is unaffected by the file store's permission problem and
	// must have actually deleted its row — a caller must be able to trust
	// that a partial success (SQL cleaned up, file leftover) is exactly
	// what gets reported, not silently upgraded to "all failed" or hidden
	// as "all succeeded".
	var n int
	if scanErr := rawDB.QueryRow(`SELECT COUNT(*) FROM workflows WHERE id = 'wf-cleanup'`).Scan(&n); scanErr != nil {
		t.Fatalf("count workflows row: %v", scanErr)
	}
	if n != 0 {
		t.Fatalf("expected the SQL row to be deleted despite the file cleanup failure, got %d rows", n)
	}

	if wf, sqlErr := sqlStore.GetWorkflow(ctx, "wf-cleanup"); sqlErr != nil || wf != nil {
		t.Fatalf("expected the SQL row gone (nil, nil), got wf=%+v err=%v", wf, sqlErr)
	}
}
