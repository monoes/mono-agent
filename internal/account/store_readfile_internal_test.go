package account

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

type refreshRead struct {
	token string
	err   error
}

func loadRefreshWithin(t *testing.T, st Store) refreshRead {
	t.Helper()
	return within(t, "LoadRefresh", func() refreshRead { rt, err := st.LoadRefresh(); return refreshRead{rt, err} })
}

// openCounter counts the calls that reach the key store to open a refresh token.
type openCounter struct {
	Sealer
	opens atomic.Int32
}

func (s *openCounter) Open(sealed []byte) ([]byte, error) {
	s.opens.Add(1)
	return s.Sealer.Open(sealed)
}

// sealedOfSize seals a refresh token so that refresh.enc is exactly size bytes:
// the version byte, the 12-byte nonce and the 16-byte tag around the token.
func sealedOfSize(t *testing.T, s Sealer, size int) []byte {
	t.Helper()
	blob, err := s.Seal(bytes.Repeat([]byte("r"), size-1-nonceSize-16))
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) != size {
		t.Fatalf("the sealed blob is %d bytes, want %d", len(blob), size)
	}
	return blob
}

// The store reads at most 64 KiB of either file: a session is a token and a few
// fields and a sealed refresh token is about a hundred bytes, so a larger file is
// not one the store wrote, and reading it whole could exhaust the memory. A file
// of exactly 64 KiB is read; one byte more is refused, even when its first 64 KiB
// would parse, and a refresh.enc that is refused never reaches the key store.
func TestTheStoreReadsAtMost64KiBOfEitherFile(t *testing.T) {
	dir := t.TempDir()
	sealer := &openCounter{Sealer: NewMemorySealer()}
	st := OpenStore(dir, sealer)
	session, refresh := filepath.Join(dir, sessionFile), filepath.Join(dir, refreshFile)

	writeFile(t, session, paddedSession(t, maxStoreFile))
	if l := loadWithin(t, st); l.err != nil || l.sess == nil || l.sess.Host != "h" {
		t.Errorf("a session.json of exactly 64 KiB: a session %v, %v, want it read", l.sess != nil, l.err)
	}
	writeFile(t, refresh, sealedOfSize(t, sealer, maxStoreFile))
	if r := loadRefreshWithin(t, st); r.err != nil || len(r.token) != maxStoreFile-1-nonceSize-16 {
		t.Errorf("a refresh.enc of exactly 64 KiB: a token of %d bytes, %v, want it read", len(r.token), r.err)
	}

	writeFile(t, session, paddedSession(t, maxStoreFile+1))
	if l := loadWithin(t, st); l.sess != nil || !errors.Is(l.err, errTooLarge) || !errors.Is(l.err, errSessionInvalid) {
		t.Errorf("a session.json one byte over 64 KiB: a session %v, %v, want the too-large refusal", l.sess != nil, l.err)
	}
	opens := sealer.opens.Load()
	writeFile(t, refresh, sealedOfSize(t, sealer, maxStoreFile+1))
	if r := loadRefreshWithin(t, st); r.token != "" || !errors.Is(r.err, errTooLarge) {
		t.Errorf("a refresh.enc one byte over 64 KiB: a token %v, %v, want the too-large refusal", r.token != "", r.err)
	}
	if n := sealer.opens.Load() - opens; n != 0 {
		t.Errorf("a refresh.enc that is too large reached the key store %d times", n)
	}
}

// The cap bounds what is read, not only what is kept: a large regular file is
// refused after 64 KiB and one byte, not read whole first. The files are sparse,
// so the test writes almost nothing to the disk.
func TestTheStoreStopsReadingAtTheCap(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{sessionFile, refreshFile} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(filepath.Join(dir, name), 64<<20); err != nil {
			t.Fatal(err)
		}
	}
	st := OpenStore(dir, NewMemorySealer())
	for _, c := range []struct {
		file string
		read func() error
	}{
		{sessionFile, func() error { _, err := st.Load(); return err }},
		{refreshFile, func() error { _, err := st.LoadRefresh(); return err }},
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := c.read()
		runtime.ReadMemStats(&after)
		if !errors.Is(err, errTooLarge) {
			t.Errorf("a %s of 64 MiB: %v, want the too-large refusal", c.file, err)
		}
		if n := after.TotalAlloc - before.TotalAlloc; n > 4<<20 {
			t.Errorf("refusing a %s of 64 MiB allocated %d bytes: it was read past the cap", c.file, n)
		}
	}
}

// A directory where a file should be is not a regular file: refused as such, for
// either file, on every platform.
func TestTheStoreRefusesADirectoryInPlaceOfAFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{sessionFile, refreshFile} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sealer := &openCounter{Sealer: NewMemorySealer()}
	st := OpenStore(dir, sealer)
	if l := loadWithin(t, st); l.sess != nil || !errors.Is(l.err, errNotRegular) || !errors.Is(l.err, errSessionInvalid) {
		t.Errorf("session.json a directory: a session %v, %v, want the not-a-regular-file refusal", l.sess != nil, l.err)
	}
	if r := loadRefreshWithin(t, st); r.token != "" || !errors.Is(r.err, errNotRegular) {
		t.Errorf("refresh.enc a directory: a token %v, %v, want the not-a-regular-file refusal", r.token != "", r.err)
	}
	if n := sealer.opens.Load(); n != 0 {
		t.Errorf("a refresh.enc that is not a regular file reached the key store %d times", n)
	}
}

// A linked folder or file is legitimate: the store follows the link, and what it
// leads to is read when it is a regular file.
func TestTheStoreFollowsALinkToARegularFile(t *testing.T) {
	real, dir := t.TempDir(), t.TempDir()
	sealer := NewMemorySealer()
	if err := OpenStore(real, sealer).Save(&Session{Host: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := OpenStore(real, sealer).SaveRefresh("rt-1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{sessionFile, refreshFile} {
		if err := os.Symlink(filepath.Join(real, name), filepath.Join(dir, name)); err != nil {
			t.Skipf("cannot make a symbolic link here: %v", err)
		}
	}
	st := OpenStore(dir, sealer)
	if l := loadWithin(t, st); l.err != nil || l.sess == nil || l.sess.Host != "h" {
		t.Errorf("a linked session.json: a session %v, %v, want it read", l.sess != nil, l.err)
	}
	if r := loadRefreshWithin(t, st); r.err != nil || r.token != "rt-1" {
		t.Errorf("a linked refresh.enc: %q, %v, want it read", r.token, r.err)
	}
}
