package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/updatecheck"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	githubOwner = "monoes"
	githubRepo  = "mono-agent"
)

// VersionInfo is returned by GetVersion.
type VersionInfo struct {
	Version   string `json:"version"`
	BuildDate string `json:"build_date"`
}

// UpdateInfo is returned by CheckForUpdate.
type UpdateInfo struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url"`
	Error           string `json:"error,omitempty"`
}

// UpdateResult is returned by SelfUpdate.
type UpdateResult struct {
	Success    bool   `json:"success"`
	NewVersion string `json:"new_version,omitempty"`
	Error      string `json:"error,omitempty"`
}

// GetVersion returns the current build version.
func (a *App) GetVersion() VersionInfo {
	return VersionInfo{
		Version:   version,
		BuildDate: buildDate,
	}
}

// CheckForUpdate queries GitHub for the latest release and compares.
func (a *App) CheckForUpdate() UpdateInfo {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return UpdateInfo{CurrentVersion: version, Error: fmt.Sprintf("network error: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return UpdateInfo{CurrentVersion: version, Error: fmt.Sprintf("GitHub API %d: %s", resp.StatusCode, string(body))}
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return UpdateInfo{CurrentVersion: version, Error: fmt.Sprintf("parse error: %v", err)}
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(version, "v")

	return UpdateInfo{
		CurrentVersion:  version,
		LatestVersion:   release.TagName,
		UpdateAvailable: latest != current && version != "dev",
		ReleaseURL:      release.HTMLURL,
	}
}

// releaseAPIURL is GitHub's latest-release endpoint (a variable so tests
// can point it at a fake release server).
var releaseAPIURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)

// SelfUpdate downloads the latest release binary, verifies it against the
// release's SHA256SUMS.txt and replaces the CLI.
// The UI app shows a dialog to restart after update.
func (a *App) SelfUpdate() UpdateResult {
	// The same resolution the app uses for every CLI call, so the update
	// replaces the binary the app actually runs.
	cliPath, err := findMonoAgentCLI()
	if err != nil {
		return UpdateResult{Error: fmt.Sprintf("cannot locate CLI binary: %v", err)}
	}
	return selfUpdate(http.DefaultClient, releaseAPIURL, cliAssetName(), cliPath, func(msg string) {
		runtime.EventsEmit(a.ctx, "update:progress", msg)
	})
}

// selfUpdate is SelfUpdate without the Wails runtime: fetch the release at
// apiURL, download assetName and the release's SHA256SUMS.txt, and install
// over cliPath only when the download's sha256 equals the manifest's entry
// for assetName — the same check `monoagentcli update` makes
// (internal/updatecheck). A missing manifest, a missing entry, a mismatch
// or any fetch failure fails closed: cliPath is left untouched.
func selfUpdate(client *http.Client, apiURL, assetName, cliPath string, progress func(string)) UpdateResult {
	// 1. Get latest release info
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return UpdateResult{Error: fmt.Sprintf("network error: %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return UpdateResult{Error: fmt.Sprintf("GitHub API %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return UpdateResult{Error: fmt.Sprintf("parse error: %v", err)}
	}

	// 2. Find the binary for this platform and the checksum manifest
	var downloadURL, sumsURL string
	for _, asset := range release.Assets {
		switch asset.Name {
		case assetName:
			downloadURL = asset.BrowserDownloadURL
		case updatecheck.SumsAssetName:
			sumsURL = asset.BrowserDownloadURL
		}
	}
	if downloadURL == "" {
		return UpdateResult{Error: fmt.Sprintf("no binary found for %s/%s (expected %s)", goruntime.GOOS, goruntime.GOARCH, assetName)}
	}
	if sumsURL == "" {
		return UpdateResult{Error: fmt.Sprintf("release %s has no %s — cannot verify the download, refusing to update", release.TagName, updatecheck.SumsAssetName)}
	}

	// 3. Download the binary and the manifest, then verify
	progress("Downloading update...")
	data, err := getAll(client, downloadURL)
	if err != nil {
		return UpdateResult{Error: fmt.Sprintf("download error: %v", err)}
	}
	sums, err := getAll(client, sumsURL)
	if err != nil {
		return UpdateResult{Error: fmt.Sprintf("fetch %s: %v — cannot verify the download, nothing was installed", updatecheck.SumsAssetName, err)}
	}
	if err := updatecheck.VerifyReleaseDigest(data, sums, assetName); err != nil {
		return UpdateResult{Error: err.Error()}
	}

	// 4. Replace the CLI binary
	progress("Installing update...")
	if err := replaceBinary(cliPath, data); err != nil {
		return UpdateResult{Error: err.Error()}
	}
	progress("Update complete!")
	return UpdateResult{
		Success:    true,
		NewVersion: release.TagName,
	}
}

// getAll fetches url; any status but 200 is an error (an HTML error page
// must never be installed as the binary).
func getAll(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// replaceBinary writes data next to cliPath and swaps it in: rename old →
// .bak, rename new → target, remove .bak (rolling back on failure). The
// temp file is next to the CLI, not in os.TempDir: /tmp is often a separate
// filesystem and a cross-device rename fails.
func replaceBinary(cliPath string, data []byte) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(cliPath), ".monoagentcli-update-*")
	if err != nil {
		return fmt.Errorf("temp file error: %v", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("download write error: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("download write error: %v", err)
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod error: %v", err)
	}
	bakPath := cliPath + ".bak"
	os.Remove(bakPath) // clean up any previous backup
	if err := os.Rename(cliPath, bakPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("backup error: %v", err)
	}
	if err := os.Rename(tmpPath, cliPath); err != nil {
		os.Rename(bakPath, cliPath) // rollback
		os.Remove(tmpPath)
		return fmt.Errorf("install error: %v", err)
	}
	os.Remove(bakPath)
	return nil
}

// cliAssetName returns the expected GitHub release asset name for the current OS/arch.
func cliAssetName() string {
	return cliAssetNameFor(goruntime.GOOS, goruntime.GOARCH)
}

// cliAssetNameFor returns the expected GitHub release asset name for the
// given GOOS/GOARCH, mirroring the binaries .github/workflows/release.yml
// publishes (darwin/linux each in amd64+arm64, windows in amd64 only; a
// windows/arm64 app gets the amd64 build, which Windows on ARM emulates). Split
// out from cliAssetName so tests can cover every platform/arch combination
// without cross-compiling or overriding runtime.GOOS/GOARCH.
func cliAssetNameFor(goos, goarch string) string {
	switch goos {
	case "darwin":
		if goarch == "arm64" {
			return "monoagentcli-darwin-arm64"
		}
		return "monoagentcli-darwin-amd64"
	case "linux":
		// No fallback to amd64 for other arches (386, riscv64, …): a wrong-
		// architecture binary would install and then fail to exec. An
		// unpublished name ends in a clear "no binary found" instead.
		return "monoagentcli-linux-" + goarch
	case "windows":
		return "monoagentcli-windows-amd64.exe"
	default:
		return "monoagentcli-" + goos + "-" + goarch
	}
}

// backgroundUpdateCheck runs once on startup (after a short delay) and then
// every 24 hours, emitting "update:available" when a newer release exists.
func (a *App) backgroundUpdateCheck() {
	time.Sleep(10 * time.Second)
	if info := a.CheckForUpdate(); info.UpdateAvailable {
		runtime.EventsEmit(a.ctx, "update:available", info)
	}

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if info := a.CheckForUpdate(); info.UpdateAvailable {
				runtime.EventsEmit(a.ctx, "update:available", info)
			}
		case <-a.ctx.Done():
			return
		}
	}
}
