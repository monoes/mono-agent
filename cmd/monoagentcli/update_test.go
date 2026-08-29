package main

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"strings"
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

func TestUpdateAssetNameMatchesPlatform(t *testing.T) {
	want := updateAssetNameFor(runtime.GOOS, runtime.GOARCH)
	if got := updateAssetName(); got != want {
		t.Errorf("updateAssetName() = %q, want %q", got, want)
	}
}

func TestParseSHA256Sums(t *testing.T) {
	const goodDigest = "658116ebf2a184e79ec1d73efb50546e1e23072bda7f9c9bcc769eb94143d2f6"
	data := strings.Join([]string{
		// Canonical release.yml format: "<digest>  <basename>" (two spaces).
		goodDigest + "  monoagentcli-darwin-arm64",
		// Binary-mode separator (space + '*') must also parse.
		goodDigest + " *monoagentcli-linux-amd64",
		// Uppercase hex is valid and normalized to lowercase.
		strings.ToUpper(goodDigest) + "  monoagentcli-windows-amd64.exe",
		// Malformed lines are skipped, not fatal.
		"garbage line with no digest",
		strings.Repeat("a", 63) + "  too-short-hash",
		strings.Repeat("z", 64) + "  non-hex-digest",
		goodDigest + "  ", // missing filename
		"",
		// Trailing CR (CRLF file) is tolerated.
		goodDigest + "  monoagentcli-darwin-amd64\r",
	}, "\n")

	sums := parseSHA256Sums([]byte(data))
	want := map[string]string{
		"monoagentcli-darwin-arm64":      goodDigest,
		"monoagentcli-linux-amd64":       goodDigest,
		"monoagentcli-windows-amd64.exe": goodDigest,
		"monoagentcli-darwin-amd64":      goodDigest,
	}
	if len(sums) != len(want) {
		t.Fatalf("parseSHA256Sums parsed %d entries (%v), want %d", len(sums), sums, len(want))
	}
	for name, digest := range want {
		if got, ok := sums[name]; !ok {
			t.Errorf("missing entry for %q (parsed: %v)", name, sums)
		} else if got != digest {
			t.Errorf("digest for %q = %q, want %q", name, got, digest)
		}
	}
}

// TestSumsFormatMatchesAssetNames pins that the names SHA256SUMS.txt
// carries (monoagentcli-<GOOS>-<GOARCH>[.exe], per release.yml's
// "sha256sum *" over flattened artifacts) are exactly what
// updateAssetNameFor computes — download and verification share one name.
func TestSumsFormatMatchesAssetNames(t *testing.T) {
	data := strings.Join([]string{
		"1111111111111111111111111111111111111111111111111111111111111111  monoagentcli-darwin-amd64",
		"2222222222222222222222222222222222222222222222222222222222222222  monoagentcli-darwin-arm64",
		"3333333333333333333333333333333333333333333333333333333333333333  monoagentcli-linux-amd64",
		"4444444444444444444444444444444444444444444444444444444444444444  monoagentcli-windows-amd64.exe",
	}, "\n")
	sums := parseSHA256Sums([]byte(data))
	for _, tc := range []struct{ goos, goarch string }{
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"linux", "amd64"}, {"windows", "amd64"},
	} {
		name := updateAssetNameFor(tc.goos, tc.goarch)
		if _, ok := sums[name]; !ok {
			t.Errorf("no SHA256SUMS entry for asset name %q (from %s/%s)", name, tc.goos, tc.goarch)
		}
	}
}

func sum256(t *testing.T, data []byte) string {
	t.Helper()
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestVerifyReleaseDigestCorrectSumPasses(t *testing.T) {
	binary := []byte("fake monoagentcli binary payload")
	sums := []byte(sum256(t, binary) + "  monoagentcli-darwin-arm64\n")
	if err := verifyReleaseDigest(binary, sums, "monoagentcli-darwin-arm64"); err != nil {
		t.Fatalf("correct digest must pass, got error: %v", err)
	}
}

func TestVerifyReleaseDigestTamperedBinaryFails(t *testing.T) {
	binary := []byte("fake monoagentcli binary payload")
	tampered := []byte("fake monoagentcli binary payload TAMPERED")
	sums := []byte(sum256(t, binary) + "  monoagentcli-darwin-arm64\n")
	err := verifyReleaseDigest(tampered, sums, "monoagentcli-darwin-arm64")
	if err == nil {
		t.Fatal("tampered binary must fail verification")
	}
	wantParts := []string{sum256(t, binary), sum256(t, tampered)}
	for _, want := range wantParts {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must print both digests; missing %q in: %v", want, err)
		}
	}
}

func TestVerifyReleaseDigestMissingAssetLineFails(t *testing.T) {
	binary := []byte("fake monoagentcli binary payload")
	// Sums file exists but has no line for our asset.
	sums := []byte(sum256(t, binary) + "  some-other-asset.zip\n")
	err := verifyReleaseDigest(binary, sums, "monoagentcli-darwin-arm64")
	if err == nil {
		t.Fatal("missing asset line must fail verification")
	}
	if !strings.Contains(err.Error(), "monoagentcli-darwin-arm64") {
		t.Errorf("error should name the missing asset, got: %v", err)
	}

	// Entirely empty/absent sums content must also hard-fail.
	if err := verifyReleaseDigest(binary, nil, "monoagentcli-darwin-arm64"); err == nil {
		t.Fatal("empty sums must fail verification")
	}
}
