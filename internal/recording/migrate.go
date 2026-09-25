package recording

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/monoes/mono-agent/internal/capture"
)

// Moving recordings out of the capture inbox (contracts §6, revised).
//
// Builds before the move wrote recording envelopes (and their spools) into
// ~/.monomind/inbox and the profiles' monomind inboxes. The first List or
// Recover in a process that finds no migration marker moves every one of
// them into its store, logging each, then writes the marker so later runs
// cost one stat.

// Logf receives migration log lines. Defaults to stderr; the bridge and
// tests replace it.
var Logf = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "recording: "+format+"\n", args...)
}

// migratedMarker sits in the unprofiled store once migration has run.
const migratedMarker = ".migrated-from-inbox"

// ensurePrivateDir creates dir (and parents) and makes dir itself 0700.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return os.Chmod(dir, 0o700)
}

// migrateLegacy runs MigrateLegacy once per store root, logging failures.
func migrateLegacy() {
	if _, err := MigrateLegacy(); err != nil {
		Logf("migration from the capture inbox failed: %v", err)
	}
}

// MigrateLegacy moves recording envelopes and spools out of the capture
// inboxes into their stores. It returns how many it moved; a no-op once the
// marker exists. A per-recording failure is logged and skipped (and the
// marker is not written, so the next run retries).
func MigrateLegacy() (int, error) {
	root, err := StoreDir("")
	if err != nil {
		return 0, err
	}
	marker := filepath.Join(root, migratedMarker)
	if _, err := os.Stat(marker); err == nil {
		return 0, nil
	}
	moved, failed := 0, 0
	for _, inbox := range legacyInboxes() {
		entries, err := capture.List(inbox)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Meta.Source != SourceRecording {
				continue
			}
			dest, err := moveToStore(e.Path, firstNonEmpty(e.Meta.Profile, profileOfLegacyInbox(inbox)))
			if err != nil {
				failed++
				Logf("could not move recording %s out of %s: %v", filepath.Base(e.Path), inbox, err)
				continue
			}
			moved++
			Logf("moved recording %s from %s to %s", filepath.Base(e.Path), inbox, filepath.Dir(dest))
		}
		spools, _ := filepath.Glob(filepath.Join(inbox, spoolPrefix+"*"))
		for _, sp := range spools {
			if _, err := moveToStore(sp, profileOfLegacyInbox(inbox)); err != nil {
				failed++
				Logf("could not move recording spool %s out of %s: %v", filepath.Base(sp), inbox, err)
				continue
			}
			Logf("moved unfinished recording %s from %s", strings.TrimPrefix(filepath.Base(sp), spoolPrefix), inbox)
		}
	}
	if failed > 0 {
		return moved, fmt.Errorf("%d recording(s) could not be moved", failed)
	}
	if err := ensurePrivateDir(root); err != nil {
		return moved, err
	}
	return moved, os.WriteFile(marker, nil, 0o600)
}

// moveToStore moves one envelope or spool directory into profile's store,
// stepping past a name already taken there.
func moveToStore(src, profile string) (string, error) {
	store, err := StoreDir(profile)
	if err != nil {
		if store, err = StoreDir(""); err != nil {
			return "", err
		}
	}
	if err := ensurePrivateDir(store); err != nil {
		return "", err
	}
	base := filepath.Base(src)
	for attempt := 0; attempt < 100; attempt++ {
		name := base
		if attempt > 0 {
			name = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		dest := filepath.Join(store, name)
		if _, err := os.Lstat(dest); err == nil {
			continue
		}
		if err := os.Rename(src, dest); err != nil {
			if !errors.Is(err, syscall.EXDEV) {
				return "", err
			}
			if err := copyTree(src, dest); err != nil {
				_ = os.RemoveAll(dest)
				return "", err
			}
			if err := os.RemoveAll(src); err != nil {
				return "", err
			}
		}
		_ = os.Chmod(dest, 0o700)
		return dest, nil
	}
	return "", fmt.Errorf("no free name for %s in %s", base, store)
}

// copyTree copies a flat envelope directory (files only) across
// filesystems, with private permissions.
func copyTree(src, dest string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		in, err := os.Open(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(dest, e.Name()), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
