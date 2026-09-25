package automation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"
)

// Install installs a .mpkg file, a package directory, or an https URL.
// The result's SHA256 is the hash of the exact bytes reviewed; pass it back
// as opts.ExpectSHA256 to install only those bytes.
// The package is validated (errors abort with ErrInvalid; the result still
// carries the review and issues), checked against the engine range and the
// policy gate (blocked → installed but disabled, with a warning). DryRun
// returns the full review, including Changes against the installed
// version, and writes nothing.
func (r *Registry) Install(src string, opts InstallOptions) (*InstallResult, error) {
	source := opts.Source
	if source == "" {
		source = SourceImported
	}
	fsys, sum, err := r.loadSource(src)
	if err != nil {
		return nil, err
	}
	if err := checkPin(sum, opts.ExpectSHA256); err != nil {
		return nil, err
	}
	p, err := OpenFS(fsys, source)
	if err != nil {
		return nil, err
	}
	files, err := readTree(fsys)
	if err != nil {
		return nil, err
	}
	res, err := r.installPackage(p, files, opts.DryRun)
	if res != nil {
		res.SHA256 = sum
	}
	return res, err
}

// ErrSHA256Mismatch is returned when InstallOptions.ExpectSHA256 does not
// match the package bytes.
var ErrSHA256Mismatch = errors.New("automation: package bytes do not match the expected sha256")

func checkPin(got, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return nil
	}
	if got == "" {
		return fmt.Errorf("%w: this source has no package hash to compare", ErrSHA256Mismatch)
	}
	if got != want {
		return fmt.Errorf("%w: expected %s, got %s (the package changed since it was reviewed)", ErrSHA256Mismatch, want, got)
	}
	return nil
}

// allowPlainHTTP lets tests install from an http:// URL. There is no
// user-facing way to set it: URL installs are https only.
var allowPlainHTTP = false

// loadSource reads src (https URL, .mpkg or directory) into a checked
// in-memory file system and returns the sha256 of the package bytes: the
// downloaded or read archive, or for a directory its deterministic pack.
func (r *Registry) loadSource(src string) (fs.FS, string, error) {
	lower := strings.ToLower(src)
	if strings.HasPrefix(lower, "http://") && !allowPlainHTTP {
		return nil, "", fmt.Errorf("refusing to install over plain http (%s): use an https URL", src)
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return r.download(src)
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, "", err
	}
	if st.IsDir() {
		fsys, err := snapshotDir(src)
		if err != nil {
			return nil, "", err
		}
		files, err := readTree(fsys)
		if err != nil {
			return nil, "", err
		}
		var buf bytes.Buffer
		if err := writeZip(files, &buf); err != nil {
			return nil, "", err
		}
		return fsys, sha256Hex(buf.Bytes()), nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	return readZipSum(f)
}

// httpClient is the client used for URL installs (tests replace its
// transport). Redirects must stay on https.
var httpClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && !allowPlainHTTP {
			return fmt.Errorf("refusing redirect to non-https URL %s", req.URL)
		}
		return nil
	},
}

func (r *Registry) download(url string) (fs.FS, string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	if resp.ContentLength > MaxArchiveBytes {
		return nil, "", fmt.Errorf("%w: download larger than %d bytes", ErrUnsafeArchive, MaxArchiveBytes)
	}
	// Spool to a scratch file under the registry so a large body is never
	// held twice, then read it through the archive checks.
	dir := r.root + string(os.PathSeparator) + ".downloads"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	tmp, err := os.CreateTemp(dir, "pkg-*.mpkg")
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, MaxArchiveBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", url, err)
	}
	if n > MaxArchiveBytes {
		return nil, "", fmt.Errorf("%w: download larger than %d bytes", ErrUnsafeArchive, MaxArchiveBytes)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	return readZipSum(tmp)
}

// installPackage validates, reviews and (unless dryRun) writes p.
func (r *Registry) installPackage(p *Package, files map[string][]byte, dryRun bool) (*InstallResult, error) {
	m := p.Manifest
	res := &InstallResult{ID: m.ID, Name: m.Name, Version: m.Version, DryRun: dryRun}
	res.Issues = Validate(p)
	res.Review = buildReview(p, files)
	allowed, reason := PolicyAllows(m)
	res.Review.PolicyBlocked, res.Review.PolicyReason = !allowed, reason
	if !allowed && p.Source != SourceBuiltin {
		res.Warnings = append(res.Warnings, "installed disabled: "+reason)
	}
	if len(res.Review.Scripts) > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("contains %d page script(s): %s", len(res.Review.Scripts), strings.Join(res.Review.Scripts, ", ")))
	}

	idx, err := r.readIndex()
	if err != nil {
		return nil, err
	}
	if e, ok := idx.Packages[m.ID]; ok && !e.Removed {
		res.PreviousVersion = e.Version
		if CompareVersions(m.Version, e.Version) < 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("downgrades %s from %s to %s", m.ID, e.Version, m.Version))
		}
		if old, err := r.Get(m.ID); err == nil {
			res.Review.Changes = reviewChanges(old, p)
		}
	}

	if HasErrors(res.Issues) {
		return res, fmt.Errorf("%w: %s", ErrInvalid, firstError(res.Issues))
	}
	if dryRun {
		return res, nil
	}

	err = r.update(func(idx *indexFile) (bool, error) {
		dir, err := r.commitLocked(idx, p, files, false)
		res.Dir = dir
		return err == nil, err
	})
	if err != nil {
		return res, err
	}
	res.Installed = true
	return res, nil
}

// commitLocked writes p's files as a new version and updates its index
// entry. The caller holds the registry lock (inside update). A package the
// policy gate blocks is installed disabled, except built-ins: those stay
// enabled and are reported unavailable, so a social build sharing this home
// sees them.
func (r *Registry) commitLocked(idx *indexFile, p *Package, files map[string][]byte, fromSeed bool) (string, error) {
	m := p.Manifest
	dir, err := r.writeVersion(m.ID, m.Version, files)
	if err != nil {
		return "", err
	}
	hash := treeHash(files)
	e, ok := idx.Packages[m.ID]
	if !ok {
		e = &indexEntry{Enabled: true}
		idx.Packages[m.ID] = e
	} else if e.Removed {
		e.Enabled = true
	}
	if e.Source != SourceBuiltin || p.Source != SourceBuiltin {
		e.SeedSha256 = "" // not (or no longer) the shipped copy
	}
	if fromSeed {
		e.SeedSha256 = hash
	}
	e.Name, e.Source, e.Trust = m.Name, p.Source, trustFor(p.Source)
	e.InstalledSha256 = hash
	allowed, reason := PolicyAllows(m)
	switch {
	case !allowed && p.Source != SourceBuiltin:
		e.Enabled, e.DisabledReason = false, reason
	case e.DisabledReason != "":
		e.Enabled, e.DisabledReason = true, "" // was policy-disabled, now allowed
	}
	if e.PendingSeedVersion != "" && CompareVersions(m.Version, e.PendingSeedVersion) >= 0 {
		e.PendingSeedVersion = ""
	}
	r.setVersion(m.ID, e, m.Version)
	return dir, nil
}

func buildReview(p *Package, files map[string][]byte) Review {
	m := p.Manifest
	rv := Review{
		Domains:       nonNil(m.Site.Domains),
		Steps:         nonNil(m.Permissions.Steps),
		Scripts:       nonNil(p.ScriptFiles()),
		Downloads:     m.Permissions.Downloads,
		Tier:          m.Policy.Tier,
		ActionEffects: map[string]string{},
		Files:         fileInfos(files),
	}
	if rv.Tier == "" {
		rv.Tier = "standard"
	}
	if m.Publisher != nil {
		rv.Publisher = m.Publisher.Name
	}
	for _, a := range m.Actions {
		effect := "undeclared"
		if def, err := p.Action(a); err == nil && def.SideEffects != "" {
			effect = def.SideEffects
		}
		rv.ActionEffects[a] = effect
	}
	return rv
}

// reviewChanges is the permission/script diff of an update.
func reviewChanges(old, next *Package) *ReviewChanges {
	ch := &ReviewChanges{
		AddedDomains: added(old.Manifest.Site.Domains, next.Manifest.Site.Domains),
		AddedSteps:   added(old.Manifest.Permissions.Steps, next.Manifest.Permissions.Steps),
	}
	oldScripts := old.ScriptFiles()
	for _, s := range next.ScriptFiles() {
		if !contains(oldScripts, s) {
			ch.AddedScripts = append(ch.AddedScripts, s)
			continue
		}
		a, _ := old.Script(s)
		b, _ := next.Script(s)
		if a != b {
			ch.ChangedScripts = append(ch.ChangedScripts, s)
		}
	}
	return ch
}

func added(old, next []string) []string {
	var out []string
	for _, s := range next {
		if !contains(old, s) {
			out = append(out, s)
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// AddAction merges one action (and its fragment/selector/script closure)
// from src into installed package id, bumping its patch version. When id
// is not installed, src's manifest (cut to that action) creates it with
// opts.Source (default local).
func (r *Registry) AddAction(id string, src *Package, actionName string, opts InstallOptions) (*InstallResult, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("automation: invalid id %q", id)
	}
	if err := checkPin(src.sha256, opts.ExpectSHA256); err != nil {
		return nil, err
	}
	srcFiles, err := readTree(src.FS)
	if err != nil {
		return nil, err
	}
	sub, err := subsetFiles(src, srcFiles, []string{actionName})
	if err != nil {
		return nil, err
	}
	subPkg, err := OpenFS(mapFS(sub), src.Source)
	if err != nil {
		return nil, err
	}

	var merged map[string][]byte
	var m Manifest
	source := opts.Source
	cur, err := r.Get(id)
	if err == nil {
		if merged, err = readTree(cur.FS); err != nil {
			return nil, err
		}
		if source == "" {
			source = cur.Source
		}
		for n, b := range sub {
			if n != ManifestFile && n != "selectors.json" {
				merged[n] = b
			}
		}
		sel, err := cur.Selectors()
		if err != nil {
			return nil, err
		}
		add, err := subPkg.Selectors()
		if err != nil {
			return nil, err
		}
		for k, e := range add {
			sel[k] = e
		}
		if len(sel) > 0 {
			b, err := json.MarshalIndent(sel, "", "  ")
			if err != nil {
				return nil, err
			}
			merged["selectors.json"] = append(b, '\n')
		}
		m = mergeManifest(cur.Manifest, subPkg.Manifest)
		m.Version = bumpPatch(cur.Manifest.Version)
	} else {
		if source == "" {
			source = SourceLocal
		}
		merged = sub
		m = subPkg.Manifest
		m.ID = id
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	merged[ManifestFile] = append(b, '\n')

	p, err := OpenFS(mapFS(merged), source)
	if err != nil {
		return nil, err
	}
	res, err := r.installPackage(p, merged, opts.DryRun)
	if res != nil {
		res.SHA256 = src.sha256
	}
	return res, err
}

// mergeManifest widens base with add's actions, permissions, scripts,
// required fragments and domains (the review's Changes shows the widening).
func mergeManifest(base, add Manifest) Manifest {
	union := func(a, b []string) []string {
		out := append([]string{}, a...)
		for _, s := range b {
			if !contains(out, s) {
				out = append(out, s)
			}
		}
		return out
	}
	base.Actions = union(base.Actions, add.Actions)
	// An empty list means unrestricted: widening it would restrict it.
	if len(base.Site.Domains) > 0 {
		base.Site.Domains = union(base.Site.Domains, add.Site.Domains)
	}
	if len(base.Permissions.Steps) > 0 {
		base.Permissions.Steps = union(base.Permissions.Steps, add.Permissions.Steps)
	}
	base.Permissions.Scripts = union(base.Permissions.Scripts, add.Permissions.Scripts)
	base.Permissions.Downloads = base.Permissions.Downloads || add.Permissions.Downloads
	if len(add.Requires.Fragments) > 0 {
		base.Requires.Fragments = union(base.Requires.Fragments, add.Requires.Fragments)
	}
	return base
}
