package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/action"
)

const indexName = "index.json"

// indexFile is <root>/index.json.
type indexFile struct {
	Version       int                    `json:"version"`
	Packages      map[string]*indexEntry `json:"packages"`
	LegacyWrapped bool                   `json:"legacyWrapped,omitempty"` // ~/.monoagent/actions wrapped once
}

type indexEntry struct {
	Name               string    `json:"name,omitempty"`
	Version            string    `json:"version"`
	Previous           string    `json:"previous,omitempty"`
	Source             string    `json:"source"`
	Trust              string    `json:"trust"`
	Enabled            bool      `json:"enabled"`
	Removed            bool      `json:"removed,omitempty"`
	SeedSha256         string    `json:"seedSha256,omitempty"`
	InstalledSha256    string    `json:"installedSha256,omitempty"`
	InstalledAt        time.Time `json:"installedAt"`
	PendingSeedVersion string    `json:"pendingSeedVersion,omitempty"`
	DisabledReason     string    `json:"disabledReason,omitempty"`
}

func trustFor(source string) string {
	switch source {
	case SourceBuiltin, SourceLocal:
		return source
	}
	return SourceImported
}

func (r *Registry) readIndex() (*indexFile, error) {
	idx := &indexFile{Version: 1, Packages: map[string]*indexEntry{}}
	b, err := os.ReadFile(filepath.Join(r.root, indexName))
	if errors.Is(err, fs.ErrNotExist) {
		return idx, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, idx); err != nil {
		return nil, fmt.Errorf("automation: corrupt %s: %w", indexName, err)
	}
	if idx.Packages == nil {
		idx.Packages = map[string]*indexEntry{}
	}
	return idx, nil
}

// update runs fn on the index under the in-process mutex and the
// cross-process file lock, writing the index back (atomically) when fn
// reports a change.
func (r *Registry) update(fn func(idx *indexFile) (changed bool, err error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(r.root, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := lockFile(lf); err != nil {
		return fmt.Errorf("automation: lock registry: %w", err)
	}
	defer unlockFile(lf)
	idx, err := r.readIndex()
	if err != nil {
		return err
	}
	changed, err := fn(idx)
	if err != nil || !changed {
		return err
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(r.root, indexName), append(b, '\n'))
}

// atomicWrite writes via a temp file in the same directory and renames it.
func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (r *Registry) versionDir(id, version string) string {
	return filepath.Join(r.root, id, version)
}

func (r *Registry) entry(id string) (*indexEntry, error) {
	idx, err := r.readIndex()
	if err != nil {
		return nil, err
	}
	e, ok := idx.Packages[id]
	if !ok || e.Removed {
		return nil, fmt.Errorf("%w: %s", ErrNotInstalled, id)
	}
	return e, nil
}

// List returns every installed package sorted by id; removed built-ins are
// included only with includeRemoved.
func (r *Registry) List(includeRemoved bool) ([]InstalledInfo, error) {
	idx, err := r.readIndex()
	if err != nil {
		return nil, err
	}
	ids := sortedIDs(idx)
	out := make([]InstalledInfo, 0, len(ids))
	for _, id := range ids {
		e := idx.Packages[id]
		if e.Removed && !includeRemoved {
			continue
		}
		out = append(out, r.info(id, e, true))
	}
	return out, nil
}

func sortedIDs(idx *indexFile) []string {
	ids := make([]string, 0, len(idx.Packages))
	for id := range idx.Packages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Info returns one installed package's row.
func (r *Registry) Info(id string) (*InstalledInfo, error) {
	e, err := r.entry(id)
	if err != nil {
		return nil, err
	}
	info := r.info(id, e, true)
	return &info, nil
}

// info builds a row; hash computes Modified (hashes the package files).
func (r *Registry) info(id string, e *indexEntry, hash bool) InstalledInfo {
	info := InstalledInfo{
		ID: id, Name: e.Name, Version: e.Version, Source: e.Source, Trust: e.Trust,
		Enabled: e.Enabled, Removed: e.Removed, InstalledAt: e.InstalledAt,
		PreviousVersion: e.Previous, PendingUpdate: e.PendingSeedVersion,
	}
	if e.Removed {
		info.Enabled = false
		info.UnavailableReason = "uninstalled"
		return info
	}
	info.Dir = r.versionDir(id, e.Version)
	p, err := OpenDir(info.Dir)
	if err != nil {
		info.UnavailableReason = fmt.Sprintf("package files unreadable: %v", err)
		return info
	}
	m := p.Manifest
	if info.Name == "" {
		info.Name = m.Name
	}
	info.Description, info.Category = m.Description, m.Category
	info.Actions = len(m.Actions)
	info.Domains, info.StartURL = m.Site.Domains, m.Site.StartURL
	info.Tier = m.Policy.Tier
	if info.Tier == "" {
		info.Tier = "standard"
	}
	info.ContainsScripts = len(m.Permissions.Scripts) > 0 || len(p.ScriptFiles()) > 0
	if m.Icon != "" && safePath(m.Icon) {
		_, statErr := fs.Stat(p.FS, m.Icon)
		info.HasIcon = statErr == nil
	}
	if hash && e.Source == SourceBuiltin && e.SeedSha256 != "" {
		if h, err := fsHash(p.FS); err == nil && h != e.SeedSha256 {
			info.Modified = true
		}
	}
	info.Available, info.UnavailableReason = availability(m)
	if info.Available && !e.Enabled && e.DisabledReason != "" {
		info.UnavailableReason = e.DisabledReason
	}
	return info
}

// Get opens the current version of an installed package.
func (r *Registry) Get(id string) (*Package, error) {
	e, err := r.entry(id)
	if err != nil {
		return nil, err
	}
	p, err := OpenDir(r.versionDir(id, e.Version))
	if err != nil {
		return nil, fmt.Errorf("automation %s: %w", id, err)
	}
	p.Source = e.Source
	p.reg = r
	return p, nil
}

// SetEnabled enables or disables a package. Enabling a package the policy
// gate blocks is an error.
func (r *Registry) SetEnabled(id string, on bool) error {
	return r.update(func(idx *indexFile) (bool, error) {
		e, ok := idx.Packages[id]
		if !ok || e.Removed {
			return false, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		if on {
			p, err := OpenDir(r.versionDir(id, e.Version))
			if err != nil {
				return false, err
			}
			if ok, reason := PolicyAllows(p.Manifest); !ok {
				return false, fmt.Errorf("cannot enable %s: %s", id, reason)
			}
		}
		if e.Enabled == on && e.DisabledReason == "" {
			return false, nil
		}
		e.Enabled, e.DisabledReason = on, ""
		return true, nil
	})
}

// Uninstall deletes a package's files. A built-in stays in the index as
// removed so seeding does not bring it back (Restore does).
func (r *Registry) Uninstall(id string) error {
	return r.update(func(idx *indexFile) (bool, error) {
		e, ok := idx.Packages[id]
		if !ok || e.Removed {
			return false, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		if !ValidID(id) {
			return false, fmt.Errorf("automation: invalid id %q", id)
		}
		if err := os.RemoveAll(filepath.Join(r.root, id)); err != nil {
			return false, err
		}
		if e.Source == SourceBuiltin {
			*e = indexEntry{Name: e.Name, Version: e.Version, Source: SourceBuiltin, Trust: SourceBuiltin,
				Removed: true, InstalledAt: e.InstalledAt}
		} else {
			delete(idx.Packages, id)
		}
		return true, nil
	})
}

// Rollback switches to the kept previous version (and keeps the current one
// as the new previous, so a rollback can be undone).
func (r *Registry) Rollback(id string) error {
	return r.update(func(idx *indexFile) (bool, error) {
		e, ok := idx.Packages[id]
		if !ok || e.Removed {
			return false, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		if e.Previous == "" {
			return false, fmt.Errorf("automation %s: no previous version to roll back to", id)
		}
		p, err := OpenDir(r.versionDir(id, e.Previous))
		if err != nil {
			return false, fmt.Errorf("automation %s: previous version %s: %w", id, e.Previous, err)
		}
		h, err := fsHash(p.FS)
		if err != nil {
			return false, err
		}
		e.Version, e.Previous = e.Previous, e.Version
		e.InstalledSha256 = h
		e.InstalledAt = time.Now().UTC()
		// A rollback is a user choice: forget the seeded hash so the next
		// Seed treats the files as the user's and only flags a pending
		// update instead of upgrading them back.
		e.SeedSha256 = ""
		return true, nil
	})
}

// WriteOverlaySelector records a healed selector in the local overlay
// (<root>/<id>/overlay/selectors.json) without touching package files.
func (r *Registry) WriteOverlaySelector(id, key string, e action.SelectorEntry) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("automation: empty selector key")
	}
	return r.update(func(idx *indexFile) (bool, error) {
		if ent, ok := idx.Packages[id]; !ok || ent.Removed {
			return false, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		dir := filepath.Join(r.root, id, "overlay")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
		ov := r.readOverlay(id)
		ov[key] = e
		b, err := json.MarshalIndent(ov, "", "  ")
		if err != nil {
			return false, err
		}
		return false, atomicWrite(filepath.Join(dir, "selectors.json"), append(b, '\n'))
	})
}

func (r *Registry) readOverlay(id string) map[string]action.SelectorEntry {
	ov := map[string]action.SelectorEntry{}
	b, err := os.ReadFile(filepath.Join(r.root, id, "overlay", "selectors.json"))
	if err == nil {
		_ = json.Unmarshal(b, &ov)
	}
	if ov == nil {
		ov = map[string]action.SelectorEntry{}
	}
	return ov
}

// writeVersion writes files as <root>/<id>/<version>, replacing that
// version if present. Files are staged beside it and renamed into place.
func (r *Registry) writeVersion(id, version string, files map[string][]byte) (string, error) {
	if !ValidID(id) || !safeName(version) {
		return "", fmt.Errorf("automation: invalid id/version %q/%q", id, version)
	}
	base := filepath.Join(r.root, id)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(base, ".staging-")
	if err != nil {
		return "", err
	}
	for name, b := range files {
		if name == ChecksumsFile {
			continue
		}
		if !safePath(name) {
			os.RemoveAll(stage)
			return "", fmt.Errorf("%w: unsafe path %q", ErrUnsafeArchive, name)
		}
		dst := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			os.RemoveAll(stage)
			return "", err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			os.RemoveAll(stage)
			return "", err
		}
	}
	target := filepath.Join(base, version)
	var old string
	if _, err := os.Stat(target); err == nil {
		old = filepath.Join(base, fmt.Sprintf(".old-%s-%d", version, time.Now().UnixNano()))
		if err := os.Rename(target, old); err != nil {
			os.RemoveAll(stage)
			return "", err
		}
	}
	if err := os.Rename(stage, target); err != nil {
		if old != "" {
			_ = os.Rename(old, target)
		}
		os.RemoveAll(stage)
		return "", err
	}
	if old != "" {
		os.RemoveAll(old)
	}
	return target, nil
}

// setVersion points e at a freshly written version, keeping the old current
// version as previous, and prunes every other version directory.
func (r *Registry) setVersion(id string, e *indexEntry, version string) {
	if e.Version != "" && e.Version != version && !e.Removed {
		e.Previous = e.Version
	}
	if e.Previous == version {
		e.Previous = ""
	}
	e.Version = version
	e.Removed = false
	e.InstalledAt = time.Now().UTC()
	r.prune(id, e.Version, e.Previous)
}

func (r *Registry) prune(id string, keep ...string) {
	base := filepath.Join(r.root, id)
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, d := range entries {
		n := d.Name()
		if !d.IsDir() || n == "overlay" {
			continue
		}
		kept := false
		for _, k := range keep {
			if k != "" && n == k {
				kept = true
			}
		}
		if !kept {
			os.RemoveAll(filepath.Join(base, n))
		}
	}
}
