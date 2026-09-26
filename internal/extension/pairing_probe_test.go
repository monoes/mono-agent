package extension

import (
	"os"
	"strconv"
	"testing"
)

func TestCheckPairing(t *testing.T) {
	_, _, _ = startCaptureServer(t) // isolated HOME; the bridge wrote its token there
	base := "http://127.0.0.1:" + os.Getenv(ExtensionPortEnv)
	if _, err := strconv.Atoi(os.Getenv(ExtensionPortEnv)); err != nil {
		t.Fatal(err)
	}
	if got, err := CheckPairing(base); got != PairingOK || err != nil {
		t.Fatalf("same token: %s, %v", got, err)
	}
	// Another HOME / a reset token: the file no longer holds the bridge's.
	path, _ := tokenPath()
	if err := os.WriteFile(path, []byte("not-the-bridges-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := CheckPairing(base); got != PairingMismatch {
		t.Fatalf("mismatched token: %s", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got, _ := CheckPairing(base); got != PairingMismatch {
		t.Fatalf("missing token: %s", got)
	}
	// Nothing listening: unknown, not mismatch.
	if got, err := CheckPairing("http://127.0.0.1:1"); got != PairingUnknown || err == nil {
		t.Fatalf("no bridge: %s, %v", got, err)
	}
}
