package secrets

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forceKeyringUnavailable stubs the keyringGet/keyringSet hooks so every OS
// keychain operation fails with a non-ErrNotFound error — the signature of a
// host with no usable keyring (headless CI, missing D-Bus). The stubs bypass
// whatever backend keyring.MockInit installed, so newSecretsTestDB's
// MockInit is irrelevant while they are active.
func forceKeyringUnavailable(t *testing.T) {
	t.Helper()
	unavailable := errors.New("keyring unavailable (forced in test)")
	origGet, origSet := keyringGet, keyringSet
	t.Cleanup(func() { keyringGet, keyringSet = origGet, origSet })
	keyringGet = func(service, user string) (string, error) {
		return "", unavailable
	}
	keyringSet = func(service, user, password string) error {
		return unavailable
	}
}

// captureFileKeyringWarns redirects the fallback's stderr warnings into a
// buffer the test can assert on.
func captureFileKeyringWarns(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := fileKeyringWarnWriter
	t.Cleanup(func() { fileKeyringWarnWriter = orig })
	fileKeyringWarnWriter = &buf
	return &buf
}

func warnCount(buf *bytes.Buffer) int {
	return strings.Count(buf.String(), "WARN:")
}

// TestFileKeyringFallback_RoundTrip is the core fallback scenario: OS
// keyring unavailable, MONOAGENT_ALLOW_FILE_KEYRING=1 set, temp HOME. A
// full add → list → get cycle must work end to end, the KEK file must be
// created exactly once with mode 0600 in a 0700 vault dir, and the warning
// must fire on every use — including a later use from a second database,
// which must reuse (not regenerate) the same file-based KEK.
func TestFileKeyringFallback_RoundTrip(t *testing.T) {
	resetKEKState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(fileKeyringEnv, "1")
	warns := captureFileKeyringWarns(t)
	forceKeyringUnavailable(t)
	ctx := context.Background()

	db := newSecretsTestDB(t)
	id, err := Add(ctx, db.DB, "default", "secret", "ci-key", map[string]string{"secret": "v-ci1"}, "", "", "ci note")
	if err != nil {
		t.Fatalf("Add under file-keyring fallback: %v", err)
	}

	entries, err := List(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("List under file-keyring fallback: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "ci-key" || entries[0].FieldCount != 1 {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	fields, notes, err := DecryptFields(ctx, db.DB, "default", id)
	if err != nil {
		t.Fatalf("DecryptFields under file-keyring fallback: %v", err)
	}
	if fields["secret"] != "v-ci1" || notes != "ci note" {
		t.Fatalf("got fields=%q notes=%q, want secret v-ci1 / ci note", fields["secret"], notes)
	}

	// The fallback is profile-scoped: one KEK file per profile under the
	// default vault dir (.file-keyring-<profileID>).
	kekPath := filepath.Join(home, ".monoagent", "vault", ".file-keyring-default")
	info, err := os.Stat(kekPath)
	if err != nil {
		t.Fatalf("KEK file not created: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("KEK file mode is %o, want 600", got)
	}
	if info.Size() != 32 {
		t.Fatalf("KEK file is %d bytes, want 32", info.Size())
	}
	dirInfo, err := os.Stat(filepath.Join(home, ".monoagent", "vault"))
	if err != nil {
		t.Fatalf("vault dir not created: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Fatalf("vault dir mode is %o, want 700", got)
	}

	if !strings.Contains(warns.String(), "file-based keyring fallback in use") {
		t.Fatalf("expected the fallback warning on stderr, got %q", warns.String())
	}

	// The KEK file must be created exactly once: a second database (a
	// stand-in for a fresh process) must reuse the same 32 bytes and warn
	// again on that use — never silently, never regenerating.
	kekBefore, err := os.ReadFile(kekPath)
	if err != nil {
		t.Fatalf("reading KEK file: %v", err)
	}
	warnsBefore := warnCount(warns)

	db2 := newSecretsTestDB(t)
	id2, err := Add(ctx, db2.DB, "default", "secret", "ci-key-2", map[string]string{"secret": "v-ci2"}, "", "", "")
	if err != nil {
		t.Fatalf("Add on second db under file-keyring fallback: %v", err)
	}
	fields2, _, err := DecryptFields(ctx, db2.DB, "default", id2)
	if err != nil {
		t.Fatalf("DecryptFields on second db under file-keyring fallback: %v", err)
	}
	if fields2["secret"] != "v-ci2" {
		t.Fatalf("second db: got %q, want v-ci2", fields2["secret"])
	}

	kekAfter, err := os.ReadFile(kekPath)
	if err != nil {
		t.Fatalf("re-reading KEK file: %v", err)
	}
	if !bytes.Equal(kekBefore, kekAfter) {
		t.Fatal("file-based KEK was regenerated on second use; it must be created once")
	}
	if warnCount(warns) <= warnsBefore {
		t.Fatal("expected a new warning for the second use of the file-based keyring")
	}
}

// TestFileKeyringFallback_FailsClosedWithoutEnv proves the fail-closed
// default: same forced keyring failure, but no MONOAGENT_ALLOW_FILE_KEYRING
// — Add must fail with the keychain error and nothing may be written under
// the vault dir.
func TestFileKeyringFallback_FailsClosedWithoutEnv(t *testing.T) {
	resetKEKState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(fileKeyringEnv, "")
	forceKeyringUnavailable(t)
	ctx := context.Background()

	db := newSecretsTestDB(t)
	_, err := Add(ctx, db.DB, "default", "secret", "ci-key", map[string]string{"secret": "v-ci1"}, "", "", "")
	if err == nil {
		t.Fatal("expected Add to fail closed without the fallback env, got nil")
	}
	if !strings.Contains(err.Error(), "reading KEK from keychain") {
		t.Fatalf("expected the keychain read error to surface unchanged, got %q", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".monoagent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no vault dir/file without the fallback env, got err %v", err)
	}
}

// TestFileKeyringFallback_WarnTextPinned freezes the exact warning string
// FD8 documents; if this changes intentionally, SECURITY.md must follow.
func TestFileKeyringFallback_WarnTextPinned(t *testing.T) {
	want := "file-based keyring fallback in use (MONOAGENT_ALLOW_FILE_KEYRING=1) — weaker than OS keychain; ensure the file is protected (disk encryption, correct permissions)"
	if fileKeyringWarn != want {
		t.Fatalf("warning text changed: %q", fileKeyringWarn)
	}
}
