package nodemgr

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ResolveVersion turns a request into a concrete version:
// "" or "lts" → newest LTS release that satisfies MinVersion,
// "24" → newest 24.x, "24.1.0" / "v24.1.0" → exactly that.
func (m *Manager) ResolveVersion(ctx context.Context, want string) (string, error) {
	want = normalize(want)
	if isVersion(want) {
		return want, nil
	}
	var index []struct {
		Version string          `json:"version"`
		LTS     json.RawMessage `json:"lts"` // false or a codename
		Files   []string        `json:"files"`
	}
	if err := m.getJSON(ctx, m.BaseURL+"/index.json", &index); err != nil {
		return "", fmt.Errorf("reading the Node release index: %w", err)
	}
	fileKey := m.indexFileKey()
	for _, rel := range index { // newest first
		v := normalize(rel.Version)
		if !isVersion(v) || !Suitable(v) || !hasString(rel.Files, fileKey) {
			continue
		}
		switch {
		case want == "" || want == "lts":
			if string(rel.LTS) == "false" {
				continue
			}
		case strings.Count(want, ".") == 0:
			if !strings.HasPrefix(v, want+".") {
				continue
			}
		default:
			return "", fmt.Errorf("invalid Node version %q", want)
		}
		return v, nil
	}
	return "", fmt.Errorf("no Node release matching %q (>= %s) for %s", want, MinVersion, fileKey)
}

// indexFileKey is how index.json names this platform's archive.
func (m *Manager) indexFileKey() string {
	switch m.GOOS {
	case "darwin":
		return "osx-" + m.nodeArch() + "-tar"
	case "windows":
		return "win-" + m.nodeArch() + "-zip"
	default:
		return m.GOOS + "-" + m.nodeArch()
	}
}

// archiveName is the distribution file for a version on this platform.
func (m *Manager) archiveName(version string) string {
	osName, ext := m.GOOS, ".tar.gz"
	if m.GOOS == "windows" {
		osName, ext = "win", ".zip"
	}
	return fmt.Sprintf("node-v%s-%s-%s%s", version, osName, m.nodeArch(), ext)
}

// Size caps against a runaway or hostile download: a Node archive is ~30-60
// MB, but v24's linux-x64 build already unpacks to 204 MB, so the unpacked
// cap leaves room for Node to keep growing.
const (
	defaultMaxDownload = 256 << 20
	defaultMaxUnpacked = 512 << 20
)

func (m *Manager) downloadCap() int64 {
	if m.maxDownload > 0 {
		return m.maxDownload
	}
	return defaultMaxDownload
}

func (m *Manager) unpackCap() int64 {
	if m.maxUnpacked > 0 {
		return m.maxUnpacked
	}
	return defaultMaxUnpacked
}

// Install downloads, verifies (the release's SHASUMS256.txt.asc against the
// pinned release keys, then the archive's SHA-256 against it) and unpacks a
// Node version, then makes it the active one. An already installed version
// is only re-activated. It holds the managed Node's lock throughout, so two
// installs at once run one after the other.
func (m *Manager) Install(ctx context.Context, want string, progress func(string)) (string, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if err := m.checkPlatform(); err != nil {
		return "", err
	}
	version, err := m.ResolveVersion(ctx, want)
	if err != nil {
		return "", err
	}
	if !Suitable(version) {
		return "", fmt.Errorf("node %s is older than the minimum %s", version, MinVersion)
	}
	unlock, err := m.lock(ctx, progress)
	if err != nil {
		return "", err
	}
	defer unlock()
	// Checked under the lock: another process may just have installed it.
	if _, err := os.Stat(m.NodePath(version)); err == nil {
		progress("node " + version + " is already installed")
		return version, m.use(version)
	}
	m.sweep()

	name := m.archiveName(version)
	base := fmt.Sprintf("%s/v%s/", m.BaseURL, version)
	asc, err := m.getBytes(ctx, base+"SHASUMS256.txt.asc", 1<<20)
	if err != nil {
		return "", fmt.Errorf("downloading checksums: %w", err)
	}
	keys := m.keys
	if keys == nil {
		if keys, err = ReleaseKeyring(); err != nil {
			return "", err
		}
	}
	sums, err := verifySums(asc, keys)
	if err != nil {
		return "", err
	}
	progress("release signature verified")
	wantSum, ok := checksumFor(sums, name)
	if !ok {
		return "", fmt.Errorf("%s is not listed in SHASUMS256.txt", name)
	}

	tmp, err := os.CreateTemp(m.Root, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	progress("downloading " + base + name)
	gotSum, err := m.download(ctx, base+name, tmp, progress)
	tmp.Close()
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", name, err)
	}
	if gotSum != wantSum {
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, gotSum, wantSum)
	}
	progress("checksum verified")

	staging, err := os.MkdirTemp(m.Root, ".unpack-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	progress("unpacking")
	if m.GOOS == "windows" {
		err = unzip(tmp.Name(), staging, m.unpackCap())
	} else {
		err = untarGz(tmp.Name(), staging, m.unpackCap())
	}
	if err != nil {
		return "", fmt.Errorf("unpacking %s: %w", name, err)
	}
	// Archives hold a single top-level node-v<ver>-<os>-<arch>/ folder.
	top := filepath.Join(staging, strings.TrimSuffix(strings.TrimSuffix(name, ".zip"), ".tar.gz"))
	dest := filepath.Join(m.Root, version)
	// Not installed (checked above), so anything here is a broken leftover.
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.Rename(top, dest); err != nil {
		return "", fmt.Errorf("installing into %s: %w", dest, err)
	}
	if got, err := m.nodeVersion(ctx, m.NodePath(version)); err != nil || got != version {
		os.RemoveAll(dest)
		return "", fmt.Errorf("installed node does not run (version %q): %v", got, err)
	}
	progress("installed node " + version + " in " + dest)
	return version, m.use(version)
}

// sweep deletes what an interrupted install or removal left in Root. It
// runs under the lock, so nothing else is using these.
func (m *Manager) sweep() {
	for _, pattern := range []string{".download-*", ".unpack-*", ".trash-*"} {
		found, _ := filepath.Glob(filepath.Join(m.Root, pattern))
		for _, f := range found {
			_ = os.RemoveAll(f)
		}
	}
}

// Prune removes every installed version except keep, and except those a
// running process uses (see removeVersion), which are left for next time.
func (m *Manager) Prune(keep string) error {
	_, err := m.PruneVersions(keep)
	return err
}

// PruneVersions is Prune, also reporting the versions it had to keep
// because they are in use.
func (m *Manager) PruneVersions(keep string) (kept []*InUseError, err error) {
	unlock, err := m.lock(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer unlock()
	versions, err := m.Installed()
	if err != nil {
		return nil, err
	}
	for _, v := range versions {
		if v == normalize(keep) {
			continue
		}
		var inUse *InUseError
		if err := m.removeVersion(v); errors.As(err, &inUse) {
			kept = append(kept, inUse)
		} else if err != nil {
			return kept, err
		}
	}
	return kept, nil
}

func checksumFor(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

func (m *Manager) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

func (m *Manager) getBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := m.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (m *Manager) getJSON(ctx context.Context, url string, v any) error {
	b, err := m.getBytes(ctx, url, 16<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// download streams url into w, reporting progress every 10%, and returns
// the SHA-256 of what it wrote. More than downloadCap bytes is an error.
func (m *Manager) download(ctx context.Context, url string, w io.Writer, progress func(string)) (string, error) {
	resp, err := m.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	limit := m.downloadCap()
	if resp.ContentLength > limit {
		return "", fmt.Errorf("%s is %d MB, over the %d MB limit", url, resp.ContentLength>>20, limit>>20)
	}
	h := sha256.New()
	pw := &progressWriter{total: resp.ContentLength, report: progress}
	n, err := io.Copy(io.MultiWriter(w, h, pw), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("%s is over the %d MB limit", url, limit>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progressWriter struct {
	total, done int64
	lastPct     int64
	report      func(string)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.total > 0 {
		pct := p.done * 100 / p.total
		if pct/10 > p.lastPct/10 {
			p.lastPct = pct
			p.report(fmt.Sprintf("downloaded %d%% (%.1f MB)", pct, float64(p.done)/(1<<20)))
		}
	}
	return len(b), nil
}

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
