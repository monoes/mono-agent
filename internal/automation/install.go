package automation

import (
	"bytes"
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
//
// Only a directory (the user's own files) may be installed with anything
// but imported source and trust (opts.Source local, or opts.Trust local or
// recorded, which default the source to local); an archive or URL always
// installs as imported. Nothing gets built-in trust except seeding.
func (r *Registry) Install(src string, opts InstallOptions) (*InstallResult, error) {
	return r.install(src, opts, false)
}

// install is Install; trustArchive lets tests install an archive with the
// source/trust it was exported with (round-trip checks).
func (r *Registry) install(src string, opts InstallOptions, trustArchive bool) (*InstallResult, error) {
	source, trust, err := installTrust(src, opts, trustArchive)
	if err != nil {
		return nil, err
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
	p.Trust = trust
	files, err := readTree(fsys)
	if err != nil {
		return nil, err
	}
	res, err := r.installPackage(p, files, opts, false)
	if res != nil {
		res.SHA256 = sum
	}
	return res, err
}

// installTrust decides the source and trust an Install records.
func installTrust(src string, opts InstallOptions, trustArchive bool) (source, trust string, err error) {
	source, trust = opts.Source, opts.Trust
	if trust != "" && !validTrust(trust) {
		return "", "", fmt.Errorf("automation: invalid trust %q", trust)
	}
	if source == "" {
		source = SourceImported
		if trust == TrustLocal || trust == TrustRecorded {
			source = SourceLocal
		}
	}
	if trustArchive {
		return source, trust, nil
	}
	if trust == TrustBuiltin || (source == SourceBuiltin && trust == "") {
		return "", "", errors.New("automation: built-in packages come only from the app itself (automation restore)")
	}
	if source != SourceImported || (trust != "" && trust != TrustImported) {
		st, statErr := os.Stat(src)
		if isURL(src) || statErr != nil || !st.IsDir() {
			return "", "", fmt.Errorf("automation: only a package directory can be installed as %s; archives and URLs install as imported", nonEmpty(trust, trustFor(source)))
		}
	}
	return source, trust, nil
}

func isURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
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

// ErrReplaces is returned when an install would overwrite an installed
// package that needs confirmation (InstallOptions.Replace): a built-in,
// local or recorded package receiving less trusted content, or the user's
// own package receiving different content.
var ErrReplaces = errors.New("automation: install would replace an installed package and needs confirmation (--replace on the command line)")

// ErrReplacesBuiltin is the former name of ErrReplaces.
var ErrReplacesBuiltin = ErrReplaces

// sameInstall reports whether p (with files) is exactly what e has
// installed: same version, content, source and trust.
func sameInstall(e *indexEntry, p *Package, files map[string][]byte) bool {
	return !e.Removed && e.Version == p.Manifest.Version && e.InstalledSha256 == treeHash(files) &&
		e.Source == p.Source && e.trust() == p.trust()
}

// ownUpgrade reports whether p is a newer version of the user's own local
// package, installed from a directory as local: an ordinary upgrade that
// needs no confirmation (the current copy stays as the rollback target).
func ownUpgrade(e *indexEntry, p *Package) bool {
	return e.Source == SourceLocal && e.trust() == TrustLocal &&
		p.Source == SourceLocal && p.trust() == TrustLocal &&
		CompareVersions(p.Manifest.Version, e.Version) > 0
}

// trustDropEffects says in plain words what a lower trust tier changes.
func trustDropEffects(to string) string {
	switch to {
	case TrustImported:
		return "page scripts stay off and real runs of actions that change things need confirmation again; secrets must be stored for this automation"
	case TrustRecorded:
		return "page scripts stay off until you allow them; secrets must be stored for this automation"
	}
	return "some defaults change"
}

// installPackage validates, reviews and (unless dryRun) writes p. merge
// marks AddAction, where recorded content may extend a built-in or local
// package (it lowers the package's trust) but imported content may not.
func (r *Registry) installPackage(p *Package, files map[string][]byte, opts InstallOptions, merge bool) (*InstallResult, error) {
	var res *InstallResult
	err := r.update(func(idx *indexFile) (bool, error) {
		var err error
		res, err = r.prepareLocked(idx, p, files, opts, merge)
		if err != nil || opts.DryRun {
			return false, err
		}
		if e, ok := idx.Packages[p.Manifest.ID]; ok && sameInstall(e, p, files) {
			// Identical content, source and trust: nothing to write.
			res.Installed, res.Dir = true, r.versionDir(p.Manifest.ID, e.Version)
			res.Warnings = append(res.Warnings, fmt.Sprintf("no changes: %s %s is already installed with this content", p.Manifest.ID, e.Version))
			return false, nil
		}
		res.Dir, err = r.commitLocked(idx, p, files, false)
		if err != nil {
			return false, err
		}
		res.Installed = true
		return true, nil
	})
	return res, err
}

// prepareLocked builds the install result for p against the index:
// validation, review, policy, what it replaces and the replace gate.
func (r *Registry) prepareLocked(idx *indexFile, p *Package, files map[string][]byte, opts InstallOptions, merge bool) (*InstallResult, error) {
	m := p.Manifest
	res := &InstallResult{ID: m.ID, Name: m.Name, Version: m.Version, DryRun: opts.DryRun}
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

	var replaceBlocked bool
	if e, ok := idx.Packages[m.ID]; ok && !e.Removed {
		incoming := p.trust()
		res.Review.Replaces = &Replaced{ID: m.ID, Source: e.Source, Trust: e.trust(), Version: e.Version}
		same := sameInstall(e, p, files)
		var why string
		switch {
		case same:
			// identical reinstall: a no-op, nothing to confirm
		case trustRank(e.trust()) >= trustRank(TrustRecorded) && incoming == TrustImported:
			why = fmt.Sprintf("REPLACES the installed %s package %q %s with imported content", strings.ToUpper(e.trust()), m.ID, e.Version)
		case trustRank(e.trust()) >= trustRank(TrustLocal) && incoming == TrustRecorded && !merge:
			why = fmt.Sprintf("REPLACES the installed %s package %q %s with recorded content", strings.ToUpper(e.trust()), m.ID, e.Version)
		case e.userOwned() && !merge && !ownUpgrade(e, p):
			why = fmt.Sprintf("REPLACES your own package %q %s with different content (the current copy is kept for rollback)", m.ID, e.Version)
		}
		if why != "" {
			res.Review.ReplaceRequired = true
			replaceBlocked = !opts.replaceConfirmed()
			res.Warnings = append(res.Warnings, why)
			if replaceBlocked {
				res.Warnings = append(res.Warnings, "replacing it requires confirmation (--replace on the command line)")
			}
		}
		if !same && trustRank(incoming) < trustRank(e.trust()) {
			res.Review.TrustChange = &TrustChange{From: e.trust(), To: incoming}
			res.Warnings = append(res.Warnings, fmt.Sprintf("trust drops from %s to %s: %s", e.trust(), incoming, trustDropEffects(incoming)))
		}
		res.PreviousVersion = e.Version
		if CompareVersions(m.Version, e.Version) < 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("downgrades %s from %s to %s", m.ID, e.Version, m.Version))
		}
		if old, err := OpenDir(r.versionDir(m.ID, e.Version)); err == nil {
			res.Review.Changes = reviewChanges(old, p)
		}
	}

	if HasErrors(res.Issues) {
		return res, fmt.Errorf("%w: %s", ErrInvalid, firstError(res.Issues))
	}
	if replaceBlocked && !opts.DryRun {
		rp := res.Review.Replaces
		return res, fmt.Errorf("%w: %s is an installed %s package (%s)", ErrReplaces, rp.ID, rp.Trust, rp.Version)
	}
	return res, nil
}

// keepReplacedLocked renames version dir v of id to a free
// "<v>+replaced.<n>" slot and returns that name.
func (r *Registry) keepReplacedLocked(id, v string) (string, error) {
	sep := "+"
	if strings.Contains(v, "+") {
		sep = "."
	}
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s%sreplaced.%d", v, sep, n)
		if _, err := os.Stat(r.versionDir(id, name)); err == nil {
			continue
		}
		if err := os.Rename(r.versionDir(id, v), r.versionDir(id, name)); err != nil {
			if os.IsNotExist(err) {
				return "", nil // nothing on disk to keep
			}
			return "", err
		}
		return name, nil
	}
}

// commitLocked writes p's files as a new version and updates its index
// entry. The caller holds the registry lock (inside update). A package the
// policy gate blocks is installed disabled, except built-ins: those stay
// enabled and are reported unavailable, so a social build sharing this home
// sees them.
func (r *Registry) commitLocked(idx *indexFile, p *Package, files map[string][]byte, fromSeed bool) (string, error) {
	m := p.Manifest
	hash := treeHash(files)
	// Same version, different content: keep the current copy as the
	// rollback target instead of overwriting the only one. (A dev-build
	// seed refresh of an unmodified built-in replaces in place.)
	var keptAs string
	if e, ok := idx.Packages[m.ID]; ok && !e.Removed && !fromSeed && e.Version == m.Version && e.InstalledSha256 != hash {
		name, err := r.keepReplacedLocked(m.ID, e.Version)
		if err != nil {
			return "", err
		}
		keptAs = name
	}
	dir, err := r.writeVersion(m.ID, m.Version, files)
	if err != nil {
		return "", err
	}
	e, ok := idx.Packages[m.ID]
	if !ok {
		e = &indexEntry{Enabled: true}
		idx.Packages[m.ID] = e
	} else if e.Removed {
		e.Enabled = true
	}
	if ok && !e.Removed && (e.Version != m.Version || keptAs != "") {
		// The current version becomes previous: remember what it was.
		e.PreviousSource, e.PreviousTrust = e.Source, e.trust()
	}
	if e.Source == SourceBuiltin && e.SeedVersion == "" && e.SeedSha256 != "" {
		e.SeedVersion = e.Version // entries seeded before seedVersion existed
	}
	if e.Source != SourceBuiltin || p.Source != SourceBuiltin {
		e.SeedSha256, e.SeedVersion = "", "" // not (or no longer) the shipped copy
	}
	if fromSeed {
		e.SeedSha256, e.SeedVersion = hash, m.Version
	}
	trust := p.trust()
	if ok && (e.trust() != trust || (e.InstalledSha256 != hash && trustRank(trust) < trustRank(TrustLocal))) {
		// New trust, or new content from a recorded/imported source: the
		// user's script and live-run opt-ins were given for something else.
		e.ScriptsAllowed, e.LiveRunConfirmed = nil, false
	}
	e.Name, e.Source, e.Trust = m.Name, p.Source, trust
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
	if keptAs != "" {
		e.Previous = keptAs // setVersion keeps it (same version) and prunes the rest
	}
	r.setVersion(m.ID, e, m.Version)
	if e.Previous == "" {
		e.PreviousSource, e.PreviousTrust = "", ""
	}
	if err := r.pruneOverlayLocked(m.ID, dir); err != nil {
		return dir, err
	}
	return dir, nil
}
