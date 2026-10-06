package account

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renameCall is one call of renameReplacingFn.
type renameCall struct{ from, to string }

// spyOnRenames replaces renameReplacingFn for the rest of the test: it records
// every call and, while *refuse is true, fails it instead of renaming. A test that
// uses it must not call t.Parallel().
func spyOnRenames(t *testing.T, refuse *bool, refused error) *[]renameCall {
	t.Helper()
	var calls []renameCall
	prev := renameReplacingFn
	renameReplacingFn = func(from, to string) error {
		calls = append(calls, renameCall{from, to})
		if *refuse {
			return refused
		}
		return prev(from, to)
	}
	t.Cleanup(func() { renameReplacingFn = prev })
	return &calls
}

// Every write of session.json and refresh.enc goes through renameReplacing: on
// Windows it is what writes the change through to the disk (MOVEFILE_WRITE_THROUGH),
// so a write that went around it could come back as the old file after a power
// cut. With every rename refused, a write fails and neither file changes, so
// nothing reaches them another way; let through, each write is exactly one rename
// of a temporary file beside the target. The guard writes these files only
// through the Store, and Save and SaveRefresh are the Store's only writes of them.
func TestEveryWriteOfTheSessionFilesGoesThroughRenameReplacing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "account")
	st := OpenStore(dir, NewMemorySealer())
	if err := st.Save(&Session{Host: "before"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRefresh("rt-before"); err != nil {
		t.Fatal(err)
	}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	writes := []struct {
		file  string
		write func() error
	}{
		{sessionFile, func() error { return st.Save(&Session{Host: "after"}) }},
		{refreshFile, func() error { return st.SaveRefresh("rt-after") }},
	}

	refuse := true
	refused := errors.New("rename refused (forced in test)")
	calls := spyOnRenames(t, &refuse, refused)
	for _, w := range writes {
		before := read(w.file)
		if err := w.write(); !errors.Is(err, refused) {
			t.Errorf("writing %s with every rename refused: err = %v, want the refusal", w.file, err)
		}
		if !bytes.Equal(read(w.file), before) {
			t.Errorf("%s changed although its rename was refused: the write went around renameReplacing", w.file)
		}
	}

	refuse = false
	for _, w := range writes {
		before, n := read(w.file), len(*calls)
		if err := w.write(); err != nil {
			t.Fatalf("writing %s: %v", w.file, err)
		}
		got := (*calls)[n:]
		if len(got) != 1 || got[0].to != filepath.Join(dir, w.file) || filepath.Dir(got[0].from) != dir || !strings.HasPrefix(filepath.Base(got[0].from), ".tmp-") {
			t.Errorf("writing %s made the renames %v, want one, of a temporary file beside it", w.file, got)
		}
		if bytes.Equal(read(w.file), before) {
			t.Errorf("%s did not change", w.file)
		}
	}
}
