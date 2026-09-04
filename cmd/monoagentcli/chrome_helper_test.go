package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type mockBridge struct {
	connected bool
}

func (m *mockBridge) IsConnected() bool {
	return m.connected
}

func TestFindLocalChromePath(t *testing.T) {
	path := findLocalChromePath()
	// If Google Chrome is installed on this machine, path should exist.
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("findLocalChromePath returned non-existent path: %s", path)
		}
	}
}

func TestIsExtensionInPreferencesFile(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Valid unpacked MonoAgent extension
	prefUnpacked := filepath.Join(tempDir, "pref_unpacked.json")
	unpackedData := `{
		"extensions": {
			"settings": {
				"abc123": {
					"path": "/path/to/my/project/chrome-extension",
					"manifest": {
						"name": "MonoAgent Bridge"
					}
				}
			}
		}
	}`
	if err := os.WriteFile(prefUnpacked, []byte(unpackedData), 0644); err != nil {
		t.Fatal(err)
	}
	if !isExtensionInPreferencesFile(prefUnpacked) {
		t.Errorf("expected true for unpacked MonoAgent extension")
	}

	// 2. Packed MonoAgent extension (by manifest name)
	prefPacked := filepath.Join(tempDir, "pref_packed.json")
	packedData := `{
		"extensions": {
			"settings": {
				"xyz789": {
					"path": "/some/other/path",
					"manifest": {
						"name": "MonoAgent Bridge",
						"description": "Browser automation bridge"
					}
				}
			}
		}
	}`
	if err := os.WriteFile(prefPacked, []byte(packedData), 0644); err != nil {
		t.Fatal(err)
	}
	if !isExtensionInPreferencesFile(prefPacked) {
		t.Errorf("expected true for packed MonoAgent extension")
	}

	// 3. Unrelated extensions only
	prefOther := filepath.Join(tempDir, "pref_other.json")
	otherData := `{
		"extensions": {
			"settings": {
				"other123": {
					"path": "/some/random/ext",
					"manifest": {
						"name": "Random AdBlocker",
						"description": "Blocks ads"
					}
				}
			}
		}
	}`
	if err := os.WriteFile(prefOther, []byte(otherData), 0644); err != nil {
		t.Fatal(err)
	}
	if isExtensionInPreferencesFile(prefOther) {
		t.Errorf("expected false for unrelated extensions")
	}

	// 4. Corrupted / invalid JSON
	prefInvalid := filepath.Join(tempDir, "pref_invalid.json")
	if err := os.WriteFile(prefInvalid, []byte("{invalid json"), 0644); err != nil {
		t.Fatal(err)
	}
	if isExtensionInPreferencesFile(prefInvalid) {
		t.Errorf("expected false for invalid json")
	}

	// 5. Non-existent file
	if isExtensionInPreferencesFile(filepath.Join(tempDir, "non_existent.json")) {
		t.Errorf("expected false for non-existent file")
	}

	// 6. Real-world unpacked shape: Chromium/Edge does not cache a "manifest"
	// object at all for a "Load unpacked" extension (verified against an
	// actual Edge profile — the settings entry has "path" but no "manifest"
	// key whatsoever), so detection must fall back to reading the real
	// manifest.json at that path from disk.
	unpackedDir := filepath.Join(tempDir, "unpacked-ext")
	if err := os.MkdirAll(unpackedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unpackedDir, "manifest.json"), []byte(`{"name": "MonoAgent Bridge"}`), 0644); err != nil {
		t.Fatal(err)
	}
	prefNoManifestCache := filepath.Join(tempDir, "pref_no_manifest_cache.json")
	noManifestCacheData := fmt.Sprintf(`{
		"extensions": {
			"settings": {
				"nniakjfndopplbofjmjgbbgljnmahpfg": {
					"path": %q
				}
			}
		}
	}`, unpackedDir)
	if err := os.WriteFile(prefNoManifestCache, []byte(noManifestCacheData), 0644); err != nil {
		t.Fatal(err)
	}
	if !isExtensionInPreferencesFile(prefNoManifestCache) {
		t.Errorf("expected true for unpacked extension with no cached manifest (must read manifest.json from path)")
	}

	// 7. Same shape, but path is a relative "<id>/<version>" segment (how
	// packed extensions record it) — must not be joined against an
	// unrelated base and must not match.
	prefRelativePath := filepath.Join(tempDir, "pref_relative_path.json")
	relativePathData := `{
		"extensions": {
			"settings": {
				"someid": {
					"path": "someid/1.0.0_0"
				}
			}
		}
	}`
	if err := os.WriteFile(prefRelativePath, []byte(relativePathData), 0644); err != nil {
		t.Fatal(err)
	}
	if isExtensionInPreferencesFile(prefRelativePath) {
		t.Errorf("expected false for a relative packed-extension path with no cached manifest")
	}
}

func TestIsExtensionInExtensionsDir(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Directory with MonoAgent manifest
	extSubDir := filepath.Join(tempDir, "ext1", "1.0.0")
	if err := os.MkdirAll(extSubDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifestData := `{"name": "MonoAgent Bridge", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(extSubDir, "manifest.json"), []byte(manifestData), 0644); err != nil {
		t.Fatal(err)
	}

	if !isExtensionInExtensionsDir(tempDir) {
		t.Errorf("expected true for valid MonoAgent extension directory")
	}

	// 2. Directory without MonoAgent manifest
	emptyDir := t.TempDir()
	otherSubDir := filepath.Join(emptyDir, "ext2", "1.0.0")
	if err := os.MkdirAll(otherSubDir, 0755); err != nil {
		t.Fatal(err)
	}
	otherManifest := `{"name": "Some Other Extension", "version": "2.0.0"}`
	if err := os.WriteFile(filepath.Join(otherSubDir, "manifest.json"), []byte(otherManifest), 0644); err != nil {
		t.Fatal(err)
	}

	if isExtensionInExtensionsDir(emptyDir) {
		t.Errorf("expected false for unrelated extension directory")
	}
}

func TestEnsureExtensionConnected_AlreadyConnected(t *testing.T) {
	bridge := &mockBridge{connected: true}
	err := ensureExtensionConnected(bridge, 100*time.Millisecond)
	if err != nil {
		t.Errorf("expected nil error when bridge is already connected, got: %v", err)
	}
}

func TestEnsureExtensionConnected_NotInstalled(t *testing.T) {
	// Set CHROME_USER_DATA_DIR to an empty temp dir and test behavior
	emptyUserDataDir := t.TempDir()
	t.Setenv("CHROME_USER_DATA_DIR", emptyUserDataDir)
	// Also override HOME (and USERPROFILE for Windows, where os.UserHomeDir
	// looks there) to an empty dir during the test so default paths aren't found
	t.Setenv("HOME", emptyUserDataDir)
	t.Setenv("USERPROFILE", emptyUserDataDir)

	bridge := &mockBridge{connected: false}
	err := ensureExtensionConnected(bridge, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when extension is not installed, got nil")
	}

	if !strings.Contains(err.Error(), "MonoAgent Chrome extension is not installed in Chrome") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// fakePairingBridge implements both connChecker and pairingURLProvider so
// tryOpenPairingPage's retry/give-up logic can be exercised without a real
// extension.Server.
type fakePairingBridge struct {
	mockBridge
	ready bool
	url   string
}

func (f *fakePairingBridge) PairingURL() (string, bool) {
	if !f.ready {
		return "", false
	}
	return f.url, true
}

func TestTryOpenPairingPage_RetriesUntilReadyThenOpensOnce(t *testing.T) {
	var openedURLs []string
	orig := openURLInBrowser
	openURLInBrowser = func(url string) error {
		openedURLs = append(openedURLs, url)
		return nil
	}
	defer func() { openURLInBrowser = orig }()

	bridge := &fakePairingBridge{ready: false, url: "http://127.0.0.1:9222/monoagent/pair?n=abc"}
	opened := false

	// Not ready yet: must not open anything, and must not set *opened so
	// the caller's poll loop keeps retrying.
	tryOpenPairingPage(bridge, &opened)
	if opened {
		t.Fatalf("opened flag set before the bridge reported ready")
	}
	if len(openedURLs) != 0 {
		t.Fatalf("openURLInBrowser called before ready: %v", openedURLs)
	}

	// Now ready: must open exactly the URL PairingURL returned, exactly once.
	bridge.ready = true
	tryOpenPairingPage(bridge, &opened)
	if !opened {
		t.Fatalf("opened flag not set after the bridge became ready")
	}
	if len(openedURLs) != 1 || openedURLs[0] != bridge.url {
		t.Fatalf("openURLInBrowser calls = %v, want exactly [%q]", openedURLs, bridge.url)
	}

	// A subsequent call (e.g. the next poll iteration) must not open again.
	tryOpenPairingPage(bridge, &opened)
	if len(openedURLs) != 1 {
		t.Fatalf("openURLInBrowser called again after already opened: %v", openedURLs)
	}
}

func TestTryOpenPairingPage_GivesUpOnBridgeWithoutPairingURL(t *testing.T) {
	var called bool
	orig := openURLInBrowser
	openURLInBrowser = func(url string) error { called = true; return nil }
	defer func() { openURLInBrowser = orig }()

	// A plain mockBridge doesn't implement pairingURLProvider at all — the
	// relay-through-another-process case (RemoteBridge in production).
	bridge := &mockBridge{connected: false}
	opened := false

	tryOpenPairingPage(bridge, &opened)
	if !opened {
		t.Fatalf("expected tryOpenPairingPage to give up immediately (set opened=true) for a bridge with no PairingURL")
	}
	if called {
		t.Fatalf("openURLInBrowser should never be called for a bridge with no PairingURL")
	}

	// Must stay given-up on repeated calls too.
	tryOpenPairingPage(bridge, &opened)
	if called {
		t.Fatalf("openURLInBrowser called on a later poll despite having given up")
	}
}
