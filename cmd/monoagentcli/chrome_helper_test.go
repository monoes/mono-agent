package main

import (
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
