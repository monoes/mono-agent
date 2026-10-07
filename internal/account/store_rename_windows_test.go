package account

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// These run on Windows only; the account-os job in .github/workflows/ci.yml runs
// them, informational until it has been green once (release.yml's Windows job
// builds and does not test).

// renameReplacing replaces an existing file, and the temporary file is gone after it.
func TestRenameReplacingReplacesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, ".tmp-1"), filepath.Join(dir, "target")
	writeFile(t, to, []byte("old"))
	writeFile(t, from, []byte("new"))
	if err := renameReplacing(from, to); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(to); err != nil || string(got) != "new" {
		t.Errorf("the target holds %q (err %v), want the new file", got, err)
	}
	if _, err := os.Stat(from); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the temporary file is still there (stat err %v)", err)
	}
}

// A reader that opened the target with delete sharing does not stop the
// replacement: it goes on reading the old file, and a new open reads the new one.
func TestRenameReplacingReplacesAFileAnotherHandleReadsWithSharing(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, ".tmp-1"), filepath.Join(dir, "target")
	writeFile(t, to, []byte("old"))
	writeFile(t, from, []byte("new"))
	name, err := windows.UTF16PtrFromString(to)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(h), to)
	defer reader.Close()
	if err := renameReplacing(from, to); err != nil {
		t.Fatalf("replacing a file another handle reads with delete sharing: %v", err)
	}
	if old, err := io.ReadAll(reader); err != nil || string(old) != "old" {
		t.Errorf("the open handle reads %q (err %v), want the old file", old, err)
	}
	if got, err := os.ReadFile(to); err != nil || string(got) != "new" {
		t.Errorf("a new open reads %q (err %v), want the new file", got, err)
	}
}

// Every write asks Windows to replace the target and to write the change through
// before the call returns: Windows has no directory flush, so without
// MOVEFILE_WRITE_THROUGH a power cut could bring back the old file.
func TestEveryWriteIsWrittenThroughOnWindows(t *testing.T) {
	var flags []uint32
	prev := moveFileEx
	moveFileEx = func(from, to *uint16, f uint32) error {
		flags = append(flags, f)
		return prev(from, to, f)
	}
	t.Cleanup(func() { moveFileEx = prev })
	st := OpenStore(filepath.Join(t.TempDir(), "account"), NewMemorySealer())
	if err := st.Save(&Session{Host: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRefresh("rt-1"); err != nil {
		t.Fatal(err)
	}
	if len(flags) != 2 {
		t.Fatalf("%d calls of MoveFileEx for a Save and a SaveRefresh, want 2", len(flags))
	}
	for _, f := range flags {
		if f&windows.MOVEFILE_REPLACE_EXISTING == 0 || f&windows.MOVEFILE_WRITE_THROUGH == 0 {
			t.Errorf("MoveFileEx flags %#x, want MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH", f)
		}
	}
}
