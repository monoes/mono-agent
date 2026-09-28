package library

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Record says where a workflow or org installed from the library came
// from, so `library update` can find newer versions. Automations keep the
// same facts in the automation registry's index (automation.LibraryOrigin).
type Record struct {
	Kind        string    `json:"kind"`
	Profile     string    `json:"profile,omitempty"` // "" for automations (shared by all profiles)
	LocalID     string    `json:"local_id"`          // workflow id | org name | automation id
	Source      string    `json:"source"`            // always "monoes"
	ItemID      string    `json:"item_id"`
	Slug        string    `json:"slug,omitempty"`
	Name        string    `json:"name,omitempty"`
	Version     string    `json:"version"`
	SHA256      string    `json:"sha256"`
	Visibility  string    `json:"visibility,omitempty"`
	Official    bool      `json:"official"`
	BaseURL     string    `json:"base_url,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
}

// Provenance is the per-machine file of Records
// (~/.monoagent/library/installed.json).
type Provenance struct {
	path string
	mu   sync.Mutex
}

type provenanceFile struct {
	V     int      `json:"v"`
	Items []Record `json:"items"`
}

// OpenProvenance returns the provenance file under the MonoAgent home
// (normally ~/.monoagent).
func OpenProvenance(home string) *Provenance {
	return &Provenance{path: filepath.Join(home, "library", "installed.json")}
}

func (p *Provenance) read() (*provenanceFile, error) {
	f := &provenanceFile{V: 1}
	b, err := os.ReadFile(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, err
	}
	return f, nil
}

func (p *Provenance) write(f *provenanceFile) error {
	sort.Slice(f.Items, func(i, j int) bool {
		a, b := f.Items[i], f.Items[j]
		if a.Profile != b.Profile {
			return a.Profile < b.Profile
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.LocalID < b.LocalID
	})
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.path), ".installed-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p.path)
}

// List returns the records for profile (and, for automations, every
// profile); kind "" means all kinds.
func (p *Provenance) List(profile, kind string) ([]Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := p.read()
	if err != nil {
		return nil, err
	}
	out := []Record{}
	for _, r := range f.Items {
		if (kind == "" || r.Kind == kind) && (r.Profile == profile || r.Profile == "") {
			out = append(out, r)
		}
	}
	return out, nil
}

// Put adds or replaces the record for (profile, kind, local id).
func (p *Provenance) Put(r Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := p.read()
	if err != nil {
		return err
	}
	r.Source = "monoes"
	kept := f.Items[:0]
	for _, x := range f.Items {
		if !(x.Profile == r.Profile && x.Kind == r.Kind && x.LocalID == r.LocalID) {
			kept = append(kept, x)
		}
	}
	f.Items = append(kept, r)
	return p.write(f)
}

// Remove drops the record for (profile, kind, local id), if any.
func (p *Provenance) Remove(profile, kind, localID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := p.read()
	if err != nil {
		return err
	}
	kept := f.Items[:0]
	for _, x := range f.Items {
		if !(x.Profile == profile && x.Kind == kind && x.LocalID == localID) {
			kept = append(kept, x)
		}
	}
	if len(kept) == len(f.Items) {
		return nil
	}
	f.Items = kept
	return p.write(f)
}
