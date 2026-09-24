package nodemgr

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
		return "osx-" + m.arch() + "-tar"
	case "windows":
		return "win-" + m.arch() + "-zip"
	default:
		return m.GOOS + "-" + m.arch()
	}
}

func (m *Manager) arch() string {
	switch m.GOARCH {
	case "amd64":
		return "x64"
	default:
		return m.GOARCH
	}
}

// archiveName is the distribution file for a version on this platform.
func (m *Manager) archiveName(version string) string {
	osName, ext := m.GOOS, ".tar.gz"
	if m.GOOS == "windows" {
		osName, ext = "win", ".zip"
	}
	return fmt.Sprintf("node-v%s-%s-%s%s", version, osName, m.arch(), ext)
}

// Install downloads, verifies (SHA-256 against the release's SHASUMS256.txt)
// and unpacks a Node version, then makes it the active one. An already
// installed version is only re-activated.
func (m *Manager) Install(ctx context.Context, want string, progress func(string)) (string, error) {
	if progress == nil {
		progress = func(string) {}
	}
	version, err := m.ResolveVersion(ctx, want)
	if err != nil {
		return "", err
	}
	if !Suitable(version) {
		return "", fmt.Errorf("node %s is older than the minimum %s", version, MinVersion)
	}
	if _, err := os.Stat(m.NodePath(version)); err == nil {
		progress("node " + version + " is already installed")
		return version, m.Use(version)
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return "", err
	}

	name := m.archiveName(version)
	base := fmt.Sprintf("%s/v%s/", m.BaseURL, version)
	sums, err := m.getBytes(ctx, base+"SHASUMS256.txt", 1<<20)
	if err != nil {
		return "", fmt.Errorf("downloading checksums: %w", err)
	}
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
		err = unzip(tmp.Name(), staging)
	} else {
		err = untarGz(tmp.Name(), staging)
	}
	if err != nil {
		return "", fmt.Errorf("unpacking %s: %w", name, err)
	}
	// Archives hold a single top-level node-v<ver>-<os>-<arch>/ folder.
	top := filepath.Join(staging, strings.TrimSuffix(strings.TrimSuffix(name, ".zip"), ".tar.gz"))
	dest := filepath.Join(m.Root, version)
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.Rename(top, dest); err != nil {
		return "", fmt.Errorf("installing into %s: %w", dest, err)
	}
	if got, err := NodeVersion(ctx, m.NodePath(version)); err != nil || got != version {
		os.RemoveAll(dest)
		return "", fmt.Errorf("installed node does not run (version %q): %v", got, err)
	}
	progress("installed node " + version + " in " + dest)
	return version, m.Use(version)
}

// Prune removes every installed version except keep.
func (m *Manager) Prune(keep string) error {
	versions, err := m.Installed()
	if err != nil {
		return err
	}
	for _, v := range versions {
		if v != normalize(keep) {
			if err := os.RemoveAll(filepath.Join(m.Root, v)); err != nil {
				return err
			}
		}
	}
	return nil
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
// the SHA-256 of what it wrote.
func (m *Manager) download(ctx context.Context, url string, w io.Writer, progress func(string)) (string, error) {
	resp, err := m.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	h := sha256.New()
	pw := &progressWriter{total: resp.ContentLength, report: progress}
	if _, err := io.Copy(io.MultiWriter(w, h, pw), resp.Body); err != nil {
		return "", err
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

// safeJoin resolves an archive entry name under dir, rejecting escapes.
func safeJoin(dir, name string) (string, error) {
	p := filepath.Join(dir, filepath.FromSlash(name))
	if p != dir && !strings.HasPrefix(p, dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the target folder", name)
	}
	return p, nil
}

func untarGz(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, os.FileMode(hdr.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Links must stay inside the tree (npm/npx → ../lib/...).
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("absolute symlink %q in archive", hdr.Name)
			}
			if _, err := safeJoin(dir, filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname)); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target, err := safeJoin(dir, zf.Name)
		if err != nil {
			return err
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, rc, 0o755)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
