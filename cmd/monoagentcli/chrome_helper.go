package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// connChecker is the minimal slice of browser.ExtensionBridge that
// ensureExtensionConnected needs.
type connChecker interface {
	IsConnected() bool
}

// pairingURLProvider is implemented only by *extension.ServerBridge (not
// the relay-through-another-process bridge) — see its doc comment for why.
// A plain interface here (rather than importing *extension.ServerBridge)
// keeps ensureExtensionConnected working against the bridge.ExtensionBridge
// abstraction it already takes.
type pairingURLProvider interface {
	PairingURL() (string, bool)
}

// openURLInBrowser opens url in the system's default browser, cross-platform.
// A var, not a plain func, so tests can stub it out — tryOpenPairingPage's
// own tests must not actually launch a browser.
var openURLInBrowser = func(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		// "start" is a cmd builtin, not an executable; the empty string
		// after it is the (required, often-empty) window-title argument —
		// without it, a URL containing spaces or quotes gets misparsed as
		// the title instead of the target.
		return exec.Command("cmd", "/c", "start", "", url).Start()
	default: // linux and other freedesktop-ish systems
		return exec.Command("xdg-open", url).Start()
	}
}

// tryOpenPairingPage opens the bridge's one-time auto-pairing page in the
// user's browser, at most once per call site (tracked via the opened
// pointer the caller owns) and only once the server has actually bound a
// port and can mint a nonce (see PairingURL). Failures are non-fatal —
// falling back to the popup's manual "Pairing token" field always works —
// so this only logs, never returns an error.
func tryOpenPairingPage(bridge connChecker, opened *bool) {
	if *opened {
		return
	}
	p, ok := bridge.(pairingURLProvider)
	if !ok {
		*opened = true // never will be ready (relay bridge) — stop trying
		return
	}
	url, ready := p.PairingURL()
	if !ready {
		return // server hasn't bound yet — the caller's poll loop retries
	}
	*opened = true
	if err := openURLInBrowser(url); err != nil {
		fmt.Fprintf(os.Stderr, "  (could not auto-open the pairing page: %v — paste the token into the extension popup manually)\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "  Opened the extension pairing page in your browser — it should pair automatically.")
	}
}

// findLocalChromePath returns the path to the local Chrome binary, or empty string.
func findLocalChromePath() string {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		programFiles := os.Getenv("ProgramFiles")
		programFilesX86 := os.Getenv("ProgramFiles(x86)")
		candidates = []string{
			filepath.Join(localAppData, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(programFiles, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(programFilesX86, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(localAppData, `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(programFiles, `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(programFilesX86, `Microsoft\Edge\Application\msedge.exe`),
		}
	default: // linux, bsd, etc.
		candidates = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome-beta",
			"/usr/bin/google-chrome-unstable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/brave-browser",
			"/usr/bin/microsoft-edge",
			"/snap/bin/chromium",
		}
	}

	for _, p := range candidates {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// isChromeRunning checks if a supported Chrome/Chromium browser process is already running.
func isChromeRunning() bool {
	switch runtime.GOOS {
	case "darwin", "linux":
		// Match the main executable binary paths to avoid false positives on helpers
		// like chrome-native-host. -i makes the match case-insensitive so
		// "Chromium"/"Google Chrome Canary"/"Brave Browser" (macOS) and the
		// lowercase Linux binary names all match one pattern.
		pattern := "(Google Chrome( Canary)?\\.app/Contents/MacOS/|" +
			"Chromium\\.app/Contents/MacOS/|" +
			"Brave Browser\\.app/Contents/MacOS/|" +
			"Microsoft Edge\\.app/Contents/MacOS/|" +
			"google-chrome|chromium|brave-browser|microsoft-edge|msedge)"
		cmd := exec.Command("pgrep", "-i", "-f", pattern)
		out, err := cmd.Output()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			return true
		}
		return false
	case "windows":
		cmd := exec.Command("tasklist")
		out, err := cmd.Output()
		if err != nil {
			return false
		}
		lower := strings.ToLower(string(out))
		return strings.Contains(lower, "chrome.exe") || strings.Contains(lower, "msedge.exe") || strings.Contains(lower, "brave.exe")
	default:
		return false
	}
}

// getExtensionDir returns the path to the chrome-extension directory to display in instructions.
func getExtensionDir() string {
	// 1. Try finding the chrome-extension directory relative to current working dir
	cwd, err := os.Getwd()
	if err == nil {
		p := filepath.Join(cwd, "chrome-extension")
		if _, err := os.Stat(filepath.Join(p, "manifest.json")); err == nil {
			return p
		}
	}
	// 2. Try ~/.monoagent/chrome-extension
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".monoagent", "chrome-extension")
		if _, err := os.Stat(filepath.Join(p, "manifest.json")); err == nil {
			return p
		}
	}
	// 3. Fallback
	return "chrome-extension"
}

// chromeUserDataDirs returns possible Chrome/Chromium user data directories.
func chromeUserDataDirs() []string {
	var dirs []string

	if custom := os.Getenv("CHROME_USER_DATA_DIR"); custom != "" {
		dirs = append(dirs, custom)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return dirs
	}

	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs,
			filepath.Join(home, "Library", "Application Support", "Google", "Chrome"),
			filepath.Join(home, "Library", "Application Support", "Google", "Chrome Canary"),
			filepath.Join(home, "Library", "Application Support", "Chromium"),
			filepath.Join(home, "Library", "Application Support", "BraveSoftware", "Brave-Browser"),
			filepath.Join(home, "Library", "Application Support", "Microsoft Edge"),
		)
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			dirs = append(dirs,
				filepath.Join(localAppData, "Google", "Chrome", "User Data"),
				filepath.Join(localAppData, "Chromium", "User Data"),
				filepath.Join(localAppData, "Microsoft", "Edge", "User Data"),
				filepath.Join(localAppData, "BraveSoftware", "Brave-Browser", "User Data"),
			)
		}
	default: // linux, etc.
		dirs = append(dirs,
			filepath.Join(home, ".config", "google-chrome"),
			filepath.Join(home, ".config", "google-chrome-beta"),
			filepath.Join(home, ".config", "google-chrome-unstable"),
			filepath.Join(home, ".config", "chromium"),
			filepath.Join(home, ".config", "BraveSoftware", "Brave-Browser"),
			filepath.Join(home, ".config", "microsoft-edge"),
		)
	}
	return dirs
}

// isExtensionInstalled scans browser profiles to verify if the MonoAgent extension is installed.
func isExtensionInstalled() bool {
	userDataDirs := chromeUserDataDirs()
	for _, baseDir := range userDataDirs {
		if _, err := os.Stat(baseDir); err != nil {
			continue
		}
		entries, err := os.ReadDir(baseDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			profileDir := filepath.Join(baseDir, entry.Name())

			// 1. Check Preferences and Secure Preferences JSON files
			for _, prefName := range []string{"Secure Preferences", "Preferences"} {
				prefPath := filepath.Join(profileDir, prefName)
				if isExtensionInPreferencesFile(prefPath) {
					return true
				}
			}

			// 2. Check Extensions directory
			extDir := filepath.Join(profileDir, "Extensions")
			if isExtensionInExtensionsDir(extDir) {
				return true
			}
		}
	}
	return false
}

// nameLooksLikeMonoAgent applies the same name/description substring match
// both call sites below need.
func nameLooksLikeMonoAgent(name, description string) bool {
	name = strings.ToLower(name)
	if strings.Contains(name, "monoagent") || strings.Contains(name, "mono-agent") || strings.Contains(name, "mono agent") {
		return true
	}
	desc := strings.ToLower(description)
	return strings.Contains(desc, "monoagent") || strings.Contains(desc, "mono-agent")
}

func isExtensionInPreferencesFile(prefPath string) bool {
	data, err := os.ReadFile(prefPath)
	if err != nil {
		return false
	}
	var root struct {
		Extensions struct {
			Settings map[string]struct {
				Path     string `json:"path"`
				Manifest struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"manifest"`
			} `json:"settings"`
		} `json:"extensions"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return false
	}

	for _, setting := range root.Extensions.Settings {
		if nameLooksLikeMonoAgent(setting.Manifest.Name, setting.Manifest.Description) {
			return true
		}
		// Chromium/Edge does not cache a "manifest" object at all for
		// extensions loaded via "Load unpacked" (verified against a real
		// profile: the entry has path set but no manifest key whatsoever) —
		// so the check above can never match a dev-mode install, which is
		// exactly how AGENTS.md/README tell users to install this extension.
		// Read the real manifest.json from disk instead. Only for an
		// absolute path (an unpacked extension's own chosen folder) — a
		// packed extension's "path" is a relative "<id>/<version>" segment
		// under the profile's own Extensions dir, already covered by
		// isExtensionInExtensionsDir, and must never be joined against an
		// arbitrary base here.
		if setting.Path == "" || !filepath.IsAbs(setting.Path) {
			continue
		}
		mData, err := os.ReadFile(filepath.Join(setting.Path, "manifest.json"))
		if err != nil {
			continue
		}
		var m struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if json.Unmarshal(mData, &m) == nil && nameLooksLikeMonoAgent(m.Name, m.Description) {
			return true
		}
	}
	return false
}

func isExtensionInExtensionsDir(extDir string) bool {
	entries, err := os.ReadDir(extDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		extSubDir := filepath.Join(extDir, entry.Name())
		verEntries, err := os.ReadDir(extSubDir)
		if err != nil {
			continue
		}
		for _, verEntry := range verEntries {
			if !verEntry.IsDir() {
				continue
			}
			manifestPath := filepath.Join(extSubDir, verEntry.Name(), "manifest.json")
			mData, err := os.ReadFile(manifestPath)
			if err != nil {
				continue
			}
			var m struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(mData, &m); err == nil {
				name := strings.ToLower(m.Name)
				if strings.Contains(name, "monoagent") || strings.Contains(name, "mono-agent") || strings.Contains(name, "mono agent") {
					return true
				}
			}
		}
	}
	return false
}

// ensureExtensionConnected returns once the extension bridge is connected.
// If the extension is not installed, it returns an immediate error without launching Chrome.
// If Chrome is already running, it waits for the extension connection without spawning extra instances.
// If Chrome is not running, it launches Chrome once and waits up to timeout for the extension to connect.
func ensureExtensionConnected(bridge connChecker, timeout time.Duration) error {
	if bridge.IsConnected() {
		return nil
	}

	// 1. Check if the extension is installed
	if !isExtensionInstalled() {
		extDir := getExtensionDir()
		return fmt.Errorf("MonoAgent Chrome extension is not installed in Chrome.\nPlease install it before running:\n  1. Open Google Chrome and go to chrome://extensions\n  2. Enable \"Developer mode\" (toggle at top right)\n  3. Click \"Load unpacked\" and select: %s\n  4. Ensure \"MonoAgent Bridge\" is enabled", extDir)
	}

	// Tracks whether we've already opened (or given up trying to open) the
	// auto-pairing page for this call — tryOpenPairingPage retries on its
	// own via the wait loops below until the bridge server has bound a
	// port and can mint a nonce, then opens at most once.
	pairingOpened := false

	// 2. Extension is installed. Check if Chrome is already running.
	if isChromeRunning() {
		// Chrome is already running, do NOT launch another Chrome instance.
		// Wait for the extension bridge to connect in case the service worker is waking up.
		deadline := time.Now().Add(timeout)
		for !bridge.IsConnected() && time.Now().Before(deadline) {
			tryOpenPairingPage(bridge, &pairingOpened)
			time.Sleep(500 * time.Millisecond)
		}
		if !bridge.IsConnected() {
			return fmt.Errorf("Chrome is running, but the MonoAgent extension did not connect within %s — make sure the extension is enabled in chrome://extensions and reload it if necessary", timeout)
		}
		return nil
	}

	// 3. Chrome is not running, so launch it once.
	chromePath := findLocalChromePath()
	if chromePath == "" {
		return fmt.Errorf("Chrome extension is installed, but Google Chrome executable was not found on this machine")
	}

	fmt.Fprintln(os.Stderr, "Chrome is not running — launching it to connect the MonoAgent extension...")
	chromeCmd := exec.Command(chromePath)
	if err := chromeCmd.Start(); err != nil {
		return fmt.Errorf("launching Chrome: %w", err)
	}
	// Reap the launched browser so it never lingers as a zombie child.
	go func() { _ = chromeCmd.Wait() }()

	deadline := time.Now().Add(timeout)
	for !bridge.IsConnected() && time.Now().Before(deadline) {
		tryOpenPairingPage(bridge, &pairingOpened)
		time.Sleep(500 * time.Millisecond)
	}
	if !bridge.IsConnected() {
		return fmt.Errorf("Chrome was opened, but the MonoAgent extension did not connect within %s — make sure it is enabled in chrome://extensions and reload it if necessary", timeout)
	}
	return nil
}
