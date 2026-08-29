package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// sha256SumsAssetName is the checksum manifest published with every
// release (see .github/workflows/release.yml "Flatten and checksum").
const sha256SumsAssetName = "SHA256SUMS.txt"

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update monoagentcli to the latest release",
		RunE:  runUpdate,
	}
}

func runUpdate(_ *cobra.Command, _ []string) error {
	fmt.Println("Checking for updates...")

	apiURL := "https://api.github.com/repos/monoes/mono-agent/releases/latest"
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(version, "v")

	if version == "dev" {
		fmt.Println("Running a dev build (no embedded version) — skipping update check.")
		return nil
	}
	if latest == current {
		fmt.Printf("Already on latest version (%s)\n", version)
		return nil
	}
	fmt.Printf("Update available: %s → %s\n", version, release.TagName)

	assetName := updateAssetName()
	var downloadURL string
	for _, a := range release.Assets {
		if a.Name == assetName {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		names := make([]string, 0, len(release.Assets))
		for _, a := range release.Assets {
			names = append(names, a.Name)
		}
		available := "(none)"
		if len(names) > 0 {
			available = strings.Join(names, ", ")
		}
		// Hard error, no architecture fallback: silently downloading an
		// amd64 binary onto an arm64 host bricks the update.
		return fmt.Errorf("no binary for %s/%s in release %s (wanted %s); available assets: %s",
			runtime.GOOS, runtime.GOARCH, release.TagName, assetName, available)
	}

	selfPath, err := selfBinaryPath()
	if err != nil {
		return fmt.Errorf("locate binary: %w", err)
	}

	// Pre-flight: the release must publish a checksum manifest, otherwise
	// the downloaded binary cannot be verified. Hard-fail like install.sh —
	// never silently skip integrity verification.
	sumsURL := ""
	for _, a := range release.Assets {
		if a.Name == sha256SumsAssetName {
			sumsURL = a.BrowserDownloadURL
			break
		}
	}
	if sumsURL == "" {
		return fmt.Errorf("release %s has no %s asset — cannot verify download integrity, refusing to update", release.TagName, sha256SumsAssetName)
	}

	fmt.Printf("Downloading %s...\n", assetName)
	data, err := httpGetAll(downloadURL)
	if err != nil {
		return err
	}

	sumsData, err := httpGetAll(sumsURL)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", sha256SumsAssetName, err)
	}

	if err := verifyReleaseDigest(data, sumsData, assetName); err != nil {
		return err
	}
	fmt.Printf("Checksum verified: %s SHA256 %s\n", assetName, sha256Hex(data))

	tmp, err := os.CreateTemp("", "monoagentcli-update-*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write download: %w", err)
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod: %w", err)
	}

	bak := selfPath + ".bak"
	os.Remove(bak)
	if err := os.Rename(selfPath, bak); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("backup: %w", err)
	}
	if err := os.Rename(tmpPath, selfPath); err != nil {
		os.Rename(bak, selfPath) // rollback
		return fmt.Errorf("install: %w", err)
	}
	os.Remove(bak)

	fmt.Printf("Updated to %s\n", release.TagName)
	return nil
}

// httpGetAll fetches url and returns the full response body. Non-200
// statuses are errors.
func httpGetAll(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download failed: GitHub returned %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// sha256Hex returns the lowercase hex-encoded SHA-256 digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// parseSHA256Sums parses the contents of a SHA256SUMS.txt file as written
// by sha256sum(1) (release.yml: "sha256sum * > SHA256SUMS.txt"): one
// "<64 hex chars>  <filename>" entry per line, where the separator is two
// spaces (text mode) or space + '*' (binary mode). Malformed lines are
// skipped; digests are normalized to lowercase.
func parseSHA256Sums(data []byte) map[string]string {
	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 66 || line[64] != ' ' {
			continue
		}
		digest := line[:64]
		name := line[65:]
		if name[0] == ' ' || name[0] == '*' {
			name = name[1:]
		}
		if name == "" || !isHex64(digest) {
			continue
		}
		sums[name] = strings.ToLower(digest)
	}
	return sums
}

func isHex64(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return len(s) == 64
}

// verifyReleaseDigest checks the downloaded bytes against the entry for
// the exact asset name in the release's SHA256SUMS.txt. A missing entry
// or a digest mismatch hard-fails (install.sh policy: never install an
// unverified binary); on mismatch both digests are reported.
func verifyReleaseDigest(data, sums []byte, assetName string) error {
	expected, ok := parseSHA256Sums(sums)[assetName]
	if !ok {
		return fmt.Errorf("integrity check failed: %s has no entry for %s — refusing to install unverified binary",
			sha256SumsAssetName, assetName)
	}
	actual := sha256Hex(data)
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("integrity check failed for %s: SHA-256 mismatch (expected %s, got %s) — download may be corrupted or tampered; nothing was installed",
			assetName, expected, actual)
	}
	return nil
}

// updateAssetName returns the release asset name for the running
// platform: monoagentcli-<GOOS>-<GOARCH> (plus ".exe" on windows) —
// uniform across platforms, with no implicit amd64 fallback.
func updateAssetName() string {
	return updateAssetNameFor(runtime.GOOS, runtime.GOARCH)
}

func updateAssetNameFor(goos, goarch string) string {
	name := "monoagentcli-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func selfBinaryPath() (string, error) {
	// Try resolving via argv[0] first (most reliable for installed binaries)
	arg0 := os.Args[0]
	if p, err := exec.LookPath(arg0); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, nil
		}
	}
	if abs, err := filepath.Abs(arg0); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf("cannot locate own binary (argv[0]=%s)", arg0)
}
