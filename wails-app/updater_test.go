package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCliAssetNameFor is a regression test for the HIGH finding at
// updater.go:199 — cliAssetName hardcoded "monoagentcli-linux-amd64" for
// every linux GOARCH, so a linux/arm64 desktop app would download the wrong
// architecture's CLI binary via SelfUpdate and fail with an exec format
// error. cliAssetNameFor mirrors the assets .github/workflows/release.yml
// actually publishes.
func TestCliAssetNameFor(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
	}{
		{"linux", "arm64", "monoagentcli-linux-arm64"},
		{"linux", "amd64", "monoagentcli-linux-amd64"},
		{"darwin", "arm64", "monoagentcli-darwin-arm64"},
		{"darwin", "amd64", "monoagentcli-darwin-amd64"},
		{"windows", "amd64", "monoagentcli-windows-amd64.exe"},
		// Unknown platforms fall back to a best-effort name rather than panicking.
		{"freebsd", "amd64", "monoagentcli-freebsd-amd64"},
		// No silent amd64 fallback for other linux arches: the unpublished
		// name makes SelfUpdate fail with "no binary found".
		{"linux", "386", "monoagentcli-linux-386"},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			if got := cliAssetNameFor(tc.goos, tc.goarch); got != tc.want {
				t.Fatalf("cliAssetNameFor(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
			}
		})
	}
}

func TestCliSiblingNames(t *testing.T) {
	for _, tc := range []struct {
		goos, goarch string
		want         []string
	}{
		{"linux", "amd64", []string{"monoagentcli", "monoagentcli-linux-amd64-bundled"}},
		{"windows", "amd64", []string{"monoagentcli.exe", "monoagentcli-windows-amd64-bundled.exe"}},
		{"darwin", "arm64", []string{"monoagentcli", "monoagentcli-darwin-arm64-bundled"}},
	} {
		got := cliSiblingNames(tc.goos, tc.goarch)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s/%s: %v, want %v", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

// TestSiblingCLIFindsBundledName: a desktop app unpacked from a release
// finds the CLI next to it under either name, preferring plain monoagentcli.
func TestSiblingCLIFindsBundledName(t *testing.T) {
	dir := t.TempDir()
	if _, ok := siblingCLI(dir, "linux", "amd64"); ok {
		t.Fatal("found a CLI in an empty dir")
	}
	bundled := filepath.Join(dir, "monoagentcli-linux-amd64-bundled")
	os.WriteFile(bundled, []byte("cli"), 0o755)
	if p, ok := siblingCLI(dir, "linux", "amd64"); !ok || p != bundled {
		t.Fatalf("bundled: %q %v", p, ok)
	}
	plain := filepath.Join(dir, "monoagentcli")
	os.WriteFile(plain, []byte("cli"), 0o755)
	if p, _ := siblingCLI(dir, "linux", "amd64"); p != plain {
		t.Fatalf("plain name should win: %q", p)
	}
	winDir := t.TempDir()
	winBundled := filepath.Join(winDir, "monoagentcli-windows-amd64-bundled.exe")
	os.WriteFile(winBundled, []byte("cli"), 0o755)
	if p, ok := siblingCLI(winDir, "windows", "amd64"); !ok || p != winBundled {
		t.Fatalf("windows bundled: %q %v", p, ok)
	}
}
