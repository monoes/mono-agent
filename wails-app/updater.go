package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
// can point it at a fake release server). AppSelfUpdate (app_update.go)
// updates the app and its bundled CLI from it.
var releaseAPIURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)

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
