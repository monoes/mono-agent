package main

import (
	"runtime"
	"testing"
)

func TestUpdateAssetNameFor(t *testing.T) {
	cases := []struct {
		name         string
		goos, goarch string
		want         string
	}{
		{"darwin arm64", "darwin", "arm64", "monoagentcli-darwin-arm64"},
		{"darwin amd64", "darwin", "amd64", "monoagentcli-darwin-amd64"},
		{"linux amd64", "linux", "amd64", "monoagentcli-linux-amd64"},
		{"linux arm64", "linux", "arm64", "monoagentcli-linux-arm64"},
		{"windows amd64", "windows", "amd64", "monoagentcli-windows-amd64.exe"},
		{"windows arm64", "windows", "arm64", "monoagentcli-windows-arm64.exe"},
	}
	for _, tc := range cases {
		if got := updateAssetNameFor(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("%s: updateAssetNameFor(%q,%q) = %q, want %q", tc.name, tc.goos, tc.goarch, got, tc.want)
		}
	}
}

// TestUpdateAssetNameMatchesPlatform pins that the no-arg wrapper derives
// from the actual runtime platform (uniform GOOS-GOARCH, never amd64 on a
// non-amd64 host).
func TestUpdateAssetNameMatchesPlatform(t *testing.T) {
	want := updateAssetNameFor(runtime.GOOS, runtime.GOARCH)
	if got := updateAssetName(); got != want {
		t.Errorf("updateAssetName() = %q, want %q", got, want)
	}
}
