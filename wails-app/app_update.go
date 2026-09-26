package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/updatecheck"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// AppSelfUpdate updates the desktop app — and the CLI bundled next to it —
// from the latest release, then quits (restarting it on macOS/Windows).
//
// Every asset is verified against the release's SHA256SUMS.txt before
// anything is written next to the app (internal/updatecheck, the same check
// `monoagentcli update` makes); a missing manifest or entry, a mismatch, a
// non-200 download or a fetch failure leaves the installation untouched.
// Files are staged next to their targets (same filesystem, so the final
// renames cannot fail with "invalid cross-device link") and swapped in
// with a rollback if any rename fails.
func (a *App) AppSelfUpdate() UpdateResult {
	exe, err := os.Executable()
	if err != nil {
		return UpdateResult{Error: "cannot determine executable path"}
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	u := appUpdater{
		client: http.DefaultClient,
		apiURL: releaseAPIURL,
		goos:   goruntime.GOOS,
		goarch: goruntime.GOARCH,
		exe:    exe,
		progress: func(msg string) {
			runtime.EventsEmit(a.ctx, "update:progress", msg)
		},
		startDetached: startDetached,
	}
	res := u.run()
	if res.Success {
		go func() {
			time.Sleep(300 * time.Millisecond)
			runtime.Quit(a.ctx)
		}()
	}
	return res
}

// appUpdater is AppSelfUpdate without the Wails runtime, so tests can run
// every OS path against a fake release server.
type appUpdater struct {
	client       *http.Client
	apiURL       string
	goos, goarch string
	exe          string // the running app binary, symlinks resolved
	progress     func(string)
	// startDetached launches the post-quit restart (macOS/Windows).
	startDetached func(name string, args ...string) error
}

// appAssetNameFor is the release asset of the desktop app on goos/goarch
// ("" when no desktop build is published for it).
func appAssetNameFor(goos, goarch string) string {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return "MonoAgent-darwin-arm64.zip"
	case goos == "windows":
		// amd64 only; Windows on ARM runs it under emulation.
		return "MonoAgent-windows-amd64.exe"
	case goos == "linux" && goarch == "amd64":
		return "MonoAgent-linux-amd64.tar.gz"
	}
	return ""
}

// bundledCLIAssetWindows is the CLI published next to the Windows app.
const bundledCLIAssetWindows = "monoagentcli-windows-amd64-bundled.exe"

type releaseInfo struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func (u appUpdater) run() UpdateResult {
	asset := appAssetNameFor(u.goos, u.goarch)
	if asset == "" {
		return UpdateResult{Error: fmt.Sprintf("app update not supported on %s/%s", u.goos, u.goarch)}
	}
	rel, err := u.fetchRelease()
	if err != nil {
		return UpdateResult{Error: err.Error()}
	}
	u.progress("Downloading app update...")
	want := []string{asset}
	// The Windows CLI is a separate asset; update it only when the app has
	// one next to it (otherwise the user runs a CLI from PATH, not ours).
	winCLI := ""
	if u.goos == "windows" {
		if p, ok := siblingCLI(filepath.Dir(u.exe), u.goos, u.goarch); ok {
			winCLI = p
			want = append(want, bundledCLIAssetWindows)
		}
	}
	files, err := u.downloadVerified(rel, want)
	if err != nil {
		return UpdateResult{Error: err.Error()}
	}

	u.progress("Installing app update...")
	switch u.goos {
	case "linux":
		err = installLinux(u.exe, files[asset])
	case "windows":
		err = u.installWindows(files[asset], winCLI, files[bundledCLIAssetWindows])
	case "darwin":
		err = u.installMacOS(files[asset])
	}
	if err != nil {
		return UpdateResult{Error: err.Error()}
	}
	u.progress("Update installed — restarting")
	return UpdateResult{Success: true, NewVersion: rel.TagName}
}

func (u appUpdater) fetchRelease() (*releaseInfo, error) {
	req, _ := http.NewRequest("GET", u.apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("parse error: %v", err)
	}
	return &rel, nil
}

// downloadVerified downloads each named asset and checks it against the
// release's SHA256SUMS.txt (exact entry per name). Any failure is an error
// and nothing is returned.
func (u appUpdater) downloadVerified(rel *releaseInfo, names []string) (map[string][]byte, error) {
	urls := map[string]string{}
	for _, a := range rel.Assets {
		urls[a.Name] = a.BrowserDownloadURL
	}
	sumsURL := urls[updatecheck.SumsAssetName]
	if sumsURL == "" {
		return nil, fmt.Errorf("release %s has no %s — cannot verify the download, refusing to update", rel.TagName, updatecheck.SumsAssetName)
	}
	for _, n := range names {
		if urls[n] == "" {
			return nil, fmt.Errorf("no app asset for %s/%s in release %s (wanted %s)", u.goos, u.goarch, rel.TagName, n)
		}
	}
	sums, err := getAll(u.client, sumsURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %v — cannot verify the download, nothing was installed", updatecheck.SumsAssetName, err)
	}
	out := map[string][]byte{}
	for _, n := range names {
		data, err := getAll(u.client, urls[n])
		if err != nil {
			return nil, fmt.Errorf("download error: %v", err)
		}
		if err := updatecheck.VerifyReleaseDigest(data, sums, n); err != nil {
			return nil, err
		}
		out[n] = data
	}
	return out, nil
}

// installWindows swaps the verified exe (and bundled CLI) in beside the
// running app. Windows lets a running exe be renamed but not deleted, so
// the old copies stay as *.bak until the restart script removes them.
func (u appUpdater) installWindows(appExe []byte, cliPath string, cliExe []byte) error {
	dir := filepath.Dir(u.exe)
	var swaps []swapItem
	staged, err := stageBytes(dir, appExe)
	if err != nil {
		return err
	}
	swaps = append(swaps, swapItem{target: u.exe, staged: staged})
	if cliPath != "" {
		s, err := stageBytes(dir, cliExe)
		if err != nil {
			os.Remove(staged)
			return err
		}
		swaps = append(swaps, swapItem{target: cliPath, staged: s})
	}
	if err := swapAll(swaps); err != nil {
		return err
	}
	var dels []string
	for _, s := range swaps {
		dels = append(dels, fmt.Sprintf("del /Q \"%s\"\r\n", s.target+".bak"))
	}
	batPath := filepath.Join(dir, ".monoagent-update.bat")
	bat := "@echo off\r\ntimeout /t 2 /nobreak > nul\r\n" + strings.Join(dels, "") +
		fmt.Sprintf("start \"\" \"%s\"\r\ndel /Q \"%%~f0\"\r\n", u.exe)
	if err := os.WriteFile(batPath, []byte(bat), 0o755); err != nil {
		return nil // installed; only the restart and cleanup are skipped
	}
	_ = u.startDetached("cmd.exe", "/C", batPath)
	return nil
}

// installMacOS unpacks the verified zip next to the .app bundle (same
// volume), swaps the bundle — which holds the bundled CLI — and reopens it
// after the app quits.
func (u appUpdater) installMacOS(zipData []byte) error {
	// exe = /path/MonoAgent.app/Contents/MacOS/<bin>: the bundle is 3 up.
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(u.exe)))
	if !strings.HasSuffix(bundle, ".app") {
		return fmt.Errorf("app is not inside a .app bundle (%s); update it by hand", bundle)
	}
	stage, err := os.MkdirTemp(filepath.Dir(bundle), ".monoagent-app-update-*")
	if err != nil {
		return fmt.Errorf("stage dir: %v", err)
	}
	defer os.RemoveAll(stage)
	zipPath := filepath.Join(stage, "app.zip")
	if err := os.WriteFile(zipPath, zipData, 0o600); err != nil {
		return fmt.Errorf("write download: %v", err)
	}
	// System unzip keeps the symlinks inside .app bundles.
	if out, err := exec.Command("unzip", "-q", zipPath, "-d", stage).CombinedOutput(); err != nil {
		return fmt.Errorf("unzip: %v — %s", err, out)
	}
	newApp := filepath.Join(stage, "MonoAgent.app")
	if st, err := os.Stat(newApp); err != nil || !st.IsDir() {
		return fmt.Errorf("MonoAgent.app not found in downloaded archive")
	}
	if err := swapAll([]swapItem{{target: bundle, staged: newApp}}); err != nil {
		return err
	}
	os.RemoveAll(bundle + ".bak")
	_ = u.startDetached("/bin/bash", "-c", "sleep 2; open "+shQuote(bundle))
	return nil
}

// shQuote wraps a string in single quotes for safe use as a POSIX shell word,
// escaping any embedded single quotes. Single-quoted strings undergo no
// $-expansion or command substitution, unlike Go's %q double-quoted form.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// startDetached starts name without waiting for it (it outlives the app).
func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec
	hideWindow(cmd)
	return cmd.Start()
}
