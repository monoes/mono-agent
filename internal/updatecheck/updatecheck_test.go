package updatecheck

import (
	"strings"
	"testing"
)

func TestVerifyReleaseDigest(t *testing.T) {
	bin := []byte("binary")
	sums := []byte(SHA256Hex(bin) + "  monoagentcli-linux-amd64\n" + SHA256Hex([]byte("x")) + " *other\n")
	if err := VerifyReleaseDigest(bin, sums, "monoagentcli-linux-amd64"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyReleaseDigest([]byte("tampered"), sums, "monoagentcli-linux-amd64"); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("mismatch: %v", err)
	}
	if err := VerifyReleaseDigest(bin, sums, "monoagentcli-linux-arm64"); err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("missing entry: %v", err)
	}
	if err := VerifyReleaseDigest(bin, nil, "monoagentcli-linux-amd64"); err == nil {
		t.Fatal("empty manifest accepted")
	}
	if got := ParseSHA256Sums(sums)["other"]; got != SHA256Hex([]byte("x")) {
		t.Fatalf("binary-mode line: %q", got)
	}
}
