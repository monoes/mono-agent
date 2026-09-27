package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// `update --app <exe>`: update the desktop app (and, on Linux, the CLI
// bundled next to it) from the latest release. Like the CLI update, the
// download must match the release's SHA256SUMS.txt or nothing is installed.
// The desktop app calls this and only shows progress; it then quits, and on
// macOS/Windows a detached script relaunches it once it has exited.

// appUpdateResult is `update --app --json` on stdout. Progress goes to stderr.
type appUpdateResult struct {
	Success    bool   `json:"success"`
	UpToDate   bool   `json:"up_to_date,omitempty"`
	NewVersion string `json:"new_version,omitempty"`
	// Restart tells the app what happens next: "quit" (the files are
	// replaced; quit and start again) or "relaunch" (a script replaces the
	// app after it quits and starts it again).
	Restart string `json:"restart,omitempty"`
	Error   string `json:"error,omitempty"`
}

// appAssetNameFor is the release asset of the desktop app for a platform.
func appAssetNameFor(goos, goarch string) string {
	switch goos {
	case "darwin":
		if goarch == "arm64" {
			return "MonoAgent-darwin-arm64.zip"
		}
		return "MonoAgent-darwin-amd64.zip"
	case "windows":
		return "MonoAgent-windows-amd64.exe"
	case "linux":
		return "MonoAgent-linux-amd64.tar.gz"
	}
	return ""
}

func runUpdateApp(cmd *cobra.Command, cfg *globalConfig, appPath, current string) error {
	progress := func(msg string) {
		if cfg.JSONOutput {
			fmt.Fprintf(cmd.ErrOrStderr(), "{\"kind\":\"line\",\"message\":%q}\n", msg)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), msg)
		}
	}
	res, err := updateApp(cmd, appPath, current, progress)
	if err != nil {
		if !cfg.JSONOutput {
			return err
		}
		res.Error = err.Error()
	}
	if cfg.JSONOutput {
		return writeJSONTo(cmd.OutOrStdout(), res)
	}
	if res.UpToDate {
		fmt.Fprintln(cmd.OutOrStdout(), "The app is already on the latest release.")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "App updated to %s — restart it (%s).\n", res.NewVersion, res.Restart)
	}
	return nil
}

func updateApp(cmd *cobra.Command, appPath, current string, progress func(string)) (appUpdateResult, error) {
	var res appUpdateResult
	if appPath == "" {
		return res, errInvalidInput("--app needs the path of the desktop app's executable")
	}
	if abs, err := filepath.EvalSymlinks(appPath); err == nil {
		appPath = abs
	}
	asset := appAssetNameFor(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		return res, fmt.Errorf("app update is not supported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	progress("Checking for updates...")
	release, err := fetchLatestRelease(cmd.Context())
	if err != nil {
		return res, err
	}
	if current != "" && !newerRelease(release.TagName, current) {
		res.Success, res.UpToDate = true, true
		return res, nil
	}
	var url, sumsURL string
	for _, a := range release.Assets {
		switch a.Name {
		case asset:
			url = a.BrowserDownloadURL
		case sha256SumsAssetName:
			sumsURL = a.BrowserDownloadURL
		}
	}
	if url == "" {
		return res, fmt.Errorf("release %s has no %s", release.TagName, asset)
	}
	if sumsURL == "" {
		return res, fmt.Errorf("release %s has no %s — cannot verify the download, refusing to update", release.TagName, sha256SumsAssetName)
	}
	progress("Downloading " + asset + "...")
	data, err := httpGetAll(url)
	if err != nil {
		return res, err
	}
	sums, err := httpGetAll(sumsURL)
	if err != nil {
		return res, fmt.Errorf("fetch %s: %w — nothing was installed", sha256SumsAssetName, err)
	}
	if err := verifyReleaseDigest(data, sums, asset); err != nil {
		return res, err
	}
	progress("Checksum verified. Installing...")
	switch runtime.GOOS {
	case "linux":
		err = installAppLinux(data, appPath)
		res.Restart = "quit"
	case "darwin":
		err = installAppDarwin(data, appPath)
		res.Restart = "relaunch"
	case "windows":
		err = installAppWindows(data, appPath)
		res.Restart = "relaunch"
	}
	if err != nil {
		return appUpdateResult{}, err
	}
	res.Success, res.NewVersion = true, release.TagName
	progress("Update installed.")
	return res, nil
}

// installAppLinux replaces the app binary from the release tarball, and the
// bundled monoagentcli next to it when there is one (the app runs that CLI).
func installAppLinux(tgz []byte, appPath string) error {
	files, err := untarGz(tgz, "MonoAgent-linux-amd64", "monoagentcli")
	if err != nil {
		return err
	}
	app, ok := files["MonoAgent-linux-amd64"]
	if !ok {
		return fmt.Errorf("MonoAgent-linux-amd64 not found in the release archive")
	}
	if err := installBinary(app, appPath); err != nil {
		return err
	}
	if cli, ok := files["monoagentcli"]; ok {
		sibling := filepath.Join(filepath.Dir(appPath), "monoagentcli")
		if _, err := os.Stat(sibling); err == nil {
			if err := installBinary(cli, sibling); err != nil {
				return fmt.Errorf("app updated, but the bundled CLI was not: %w", err)
			}
		}
	}
	return nil
}

// untarGz returns the named regular files of a .tar.gz, by base name.
func untarGz(data []byte, names ...string) (map[string][]byte, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("release archive: %w", err)
	}
	tr := tar.NewReader(zr)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("release archive: %w", err)
		}
		base := filepath.Base(h.Name)
		if h.Typeflag != tar.TypeReg || !want[base] {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 512<<20))
		if err != nil {
			return nil, fmt.Errorf("release archive: %w", err)
		}
		out[base] = b
	}
}

// installAppDarwin unpacks the .app and hands the swap to a detached script
// that waits for the app to quit, replaces the bundle and opens it again.
func installAppDarwin(zip []byte, exe string) error {
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // …/MonoAgent.app/Contents/MacOS/<exe>
	extract, err := os.MkdirTemp("", "monoagent-app-extract-*")
	if err != nil {
		return err
	}
	zipPath := filepath.Join(extract, "update.zip")
	if err := os.WriteFile(zipPath, zip, 0o600); err != nil {
		return err
	}
	// System unzip keeps the symlinks inside .app bundles.
	if out, err := exec.Command("unzip", "-q", zipPath, "-d", extract).CombinedOutput(); err != nil {
		return fmt.Errorf("unzip: %v — %s", err, out)
	}
	newApp := filepath.Join(extract, "MonoAgent.app")
	if _, err := os.Stat(newApp); err != nil {
		return fmt.Errorf("MonoAgent.app not found in the release archive")
	}
	script := filepath.Join(extract, "update.sh")
	if err := os.WriteFile(script, []byte(darwinSwapScript(bundle, newApp, extract)), 0o700); err != nil {
		return err
	}
	return detachStart(exec.Command("bash", script))
}

// darwinSwapScript single-quotes every path: a path with $ or ` must not
// expand or execute.
func darwinSwapScript(bundle, newApp, workDir string) string {
	return fmt.Sprintf("#!/bin/bash\nsleep 2\nrm -rf %s\ncp -R %s %s\nopen %s\nrm -rf %s\n",
		shQuote(bundle), shQuote(newApp), shQuote(bundle), shQuote(bundle), shQuote(workDir))
}

// installAppWindows stages the new exe and hands the swap to a detached
// batch file that waits for the app to quit, moves it in and starts it.
func installAppWindows(exeData []byte, exe string) error {
	staged := exe + ".new"
	if err := os.WriteFile(staged, exeData, 0o755); err != nil {
		return err
	}
	bat := filepath.Join(os.TempDir(), "monoagent-update.bat")
	if err := os.WriteFile(bat, []byte(windowsSwapScript(staged, exe, bat)), 0o755); err != nil {
		return err
	}
	return detachStart(exec.Command("cmd.exe", "/C", bat))
}

func windowsSwapScript(staged, exe, bat string) string {
	return fmt.Sprintf("@echo off\r\ntimeout /t 2 /nobreak > nul\r\nmove /Y \"%s\" \"%s\"\r\nstart \"\" \"%s\"\r\ndel /Q \"%s\"\r\n",
		staged, exe, exe, bat)
}

// shQuote wraps s in single quotes for a POSIX shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
