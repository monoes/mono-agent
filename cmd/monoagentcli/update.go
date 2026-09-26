package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/monoes/mono-agent/internal/updatecheck"
	"github.com/spf13/cobra"
)

// sha256SumsAssetName is the checksum manifest published with every
// release (see .github/workflows/release.yml "Flatten and checksum").
const sha256SumsAssetName = updatecheck.SumsAssetName

func newUpdateCmd(cfg *globalConfig) *cobra.Command {
	var check bool
	var current string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update monoagentcli to the latest release",
		Long: "Downloads the latest release, verifies it against the release's SHA256SUMS.txt and replaces this binary.\n\n" +
			"--check only reports whether a newer release exists (nothing is downloaded). --current compares against another " +
			"version instead of this binary's — the desktop app asks about its own version this way.",
		Example: `  monoagentcli update
  monoagentcli --json update --check
  monoagentcli --json update --check --current v0.72.0`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if check {
				return runUpdateCheck(cmd, cfg, current)
			}
			if current != "" {
				return errInvalidInput("--current only goes with --check")
			}
			return runUpdate(cmd, args)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Only report whether a newer release exists; download nothing")
	cmd.Flags().StringVar(&current, "current", "", "Version to compare against (default: this binary's)")
	return cmd
}

// updateCheck is `update --check --json`.
type updateCheck struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url"`
	Error           string `json:"error,omitempty"`
}

// runUpdateCheck reports the latest release. A release that cannot be
// fetched is a result (error field, exit 0) in --json mode — the caller is
// polling, not failing.
func runUpdateCheck(cmd *cobra.Command, cfg *globalConfig, current string) error {
	if current == "" {
		current = getVersion()
	}
	res := updateCheck{CurrentVersion: current}
	release, err := fetchLatestRelease(cmd.Context())
	if err != nil {
		if !cfg.JSONOutput {
			return err
		}
		res.Error = err.Error()
		return writeJSONTo(cmd.OutOrStdout(), res)
	}
	res.LatestVersion = release.TagName
	res.ReleaseURL = release.HTMLURL
	res.UpdateAvailable = newerRelease(release.TagName, current)
	if cfg.JSONOutput {
		return writeJSONTo(cmd.OutOrStdout(), res)
	}
	out := cmd.OutOrStdout()
	switch {
	case res.UpdateAvailable:
		fmt.Fprintf(out, "Update available: %s → %s\n%s\n", current, release.TagName, release.HTMLURL)
	case isDevVersion(current):
		fmt.Fprintf(out, "Latest release is %s (this is a dev build)\n", release.TagName)
	default:
		fmt.Fprintf(out, "Already on the latest version (%s)\n", current)
	}
	return nil
}

// isDevVersion reports a build with no release version: "dev", or a git
// describe of a commit past a tag ("v0.72.0-3-gabc123").
func isDevVersion(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || v == "dev" || strings.Contains(v, "-g")
}

// newerRelease reports whether latest is a newer release than current,
// comparing dotted numbers (v0.10.0 > v0.9.9). Dev builds never are.
func newerRelease(latest, current string) bool {
	if isDevVersion(current) || latest == "" {
		return false
	}
	a := strings.Split(strings.TrimPrefix(strings.TrimSpace(latest), "v"), ".")
	b := strings.Split(strings.TrimPrefix(strings.TrimSpace(current), "v"), ".")
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := versionPart(a, i), versionPart(b, i)
		if x != y {
			return x > y
		}
	}
	return false
}

func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n := 0
	for _, r := range parts[i] {
		if r < '0' || r > '9' {
			break // "0-rc1" compares as 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func runUpdate(_ *cobra.Command, _ []string) error {
	fmt.Println("Checking for updates...")

	release, err := fetchLatestRelease(context.Background())
	if err != nil {
		return err
	}

	v := getVersion()
	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(v, "v")

	if v == "dev" {
		fmt.Println("Running a dev build (no embedded version) — skipping update check.")
		return nil
	}
	if latest == current {
		fmt.Printf("Already on latest version (%s)\n", v)
		return nil
	}
	fmt.Printf("Update available: %s → %s\n", v, release.TagName)

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

	if err := installBinary(data, selfPath); err != nil {
		return err
	}

	fmt.Printf("Updated to %s\n", release.TagName)
	return nil
}

// httpGetAll fetches url and returns the full response body. Non-200
// statuses are errors.
// latestRelease is the part of GitHub's latest-release payload we use.
type latestRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetchLatestRelease asks GitHub for mono-agent's latest release.
// latestReleaseURL is GitHub's latest-release endpoint (a variable so tests
// can point it at a fake server).
var latestReleaseURL = "https://api.github.com/repos/monoes/mono-agent/releases/latest"

func fetchLatestRelease(ctx context.Context) (*latestRelease, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", latestReleaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release latestRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &release, nil
}

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

// The checksum helpers live in internal/updatecheck, shared with the
// desktop app's updater (wails-app/updater.go).
var (
	sha256Hex           = updatecheck.SHA256Hex
	parseSHA256Sums     = updatecheck.ParseSHA256Sums
	verifyReleaseDigest = updatecheck.VerifyReleaseDigest
)

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

// installBinary atomically replaces target with data. The temp file is
// created next to target, not in os.TempDir: /tmp is often a separate
// filesystem (tmpfs), and a rename across filesystems fails with
// "invalid cross-device link".
func installBinary(data []byte, target string) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".monoagentcli-update-*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write download: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write download: %w", err)
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod: %w", err)
	}

	bak := target + ".bak"
	os.Remove(bak)
	if err := os.Rename(target, bak); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("backup: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Rename(bak, target) // rollback
		os.Remove(tmpPath)
		return fmt.Errorf("install: %w", err)
	}
	os.Remove(bak)
	return nil
}
