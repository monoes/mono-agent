// Package updatecheck verifies downloaded release binaries against the
// release's SHA256SUMS.txt. It is shared by `monoagentcli update`
// (cmd/monoagentcli/update.go) and the desktop app's updater
// (wails-app/updater.go), so both refuse an unverified binary the same way.
package updatecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// SumsAssetName is the checksum manifest published with every release
// (see .github/workflows/release.yml "Flatten and checksum").
const SumsAssetName = "SHA256SUMS.txt"

// SHA256Hex returns the lowercase hex-encoded SHA-256 digest of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ParseSHA256Sums parses the contents of a SHA256SUMS.txt file as written
// by sha256sum(1) (release.yml: "sha256sum * > SHA256SUMS.txt"): one
// "<64 hex chars>  <filename>" entry per line, where the separator is two
// spaces (text mode) or space + '*' (binary mode). Malformed lines are
// skipped; digests are normalized to lowercase.
func ParseSHA256Sums(data []byte) map[string]string {
	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 66 || line[64] != ' ' {
			continue
		}
		digest := line[:64]
		name := line[65:]
		if name[0] == ' ' || name[0] == '*' {
			name = name[1:]
		}
		if name == "" || !isHex64(digest) {
			continue
		}
		sums[name] = strings.ToLower(digest)
	}
	return sums
}

func isHex64(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return len(s) == 64
}

// VerifyReleaseDigest checks the downloaded bytes against the entry for
// the exact asset name in the release's SHA256SUMS.txt. A missing entry
// or a digest mismatch hard-fails (install.sh policy: never install an
// unverified binary); on mismatch both digests are reported.
func VerifyReleaseDigest(data, sums []byte, assetName string) error {
	expected, ok := ParseSHA256Sums(sums)[assetName]
	if !ok {
		return fmt.Errorf("integrity check failed: %s has no entry for %s — refusing to install unverified binary",
			SumsAssetName, assetName)
	}
	actual := SHA256Hex(data)
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("integrity check failed for %s: SHA-256 mismatch (expected %s, got %s) — download may be corrupted or tampered; nothing was installed",
			assetName, expected, actual)
	}
	return nil
}
