package main

import "testing"

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
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			if got := cliAssetNameFor(tc.goos, tc.goarch); got != tc.want {
				t.Fatalf("cliAssetNameFor(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
			}
		})
	}
}
