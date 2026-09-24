package nodemgr

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// Real SHASUMS256.txt.asc files from nodejs.org verify offline against the
// pinned keys: RSA (v24.11.0), EdDSA (v25.1.0) and an EdDSA subkey (v24.12.0).
func TestVerifyRealReleaseSums(t *testing.T) {
	keys, err := ReleaseKeyring()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) < 20 {
		t.Fatalf("only %d pinned keys parsed", len(keys))
	}
	for _, v := range []string{"24.11.0", "25.1.0", "24.12.0"} {
		asc, err := os.ReadFile(filepath.Join("testdata", "SHASUMS256-v"+v+".txt.asc"))
		if err != nil {
			t.Fatal(err)
		}
		sums, err := verifySums(asc, keys)
		if err != nil {
			t.Fatalf("v%s: %v", v, err)
		}
		if _, ok := checksumFor(sums, "node-v"+v+"-linux-x64.tar.gz"); !ok {
			t.Fatalf("v%s: linux-x64 checksum missing from the signed text", v)
		}
		// One changed checksum digit breaks the signature.
		i := bytes.Index(asc, []byte("  node-v"+v+"-linux-x64.tar.gz")) - 1
		bad := bytes.Clone(asc)
		bad[i] ^= 1
		if _, err := verifySums(bad, keys); err == nil {
			t.Fatalf("v%s: a tampered checksum verified", v)
		}
	}
}

// A message signed by a key that is not pinned is refused, and so is a
// file that isn't clearsigned at all.
func TestVerifyRejectsUnknownSigner(t *testing.T) {
	keys, err := ReleaseKeyring()
	if err != nil {
		t.Fatal(err)
	}
	stranger := testSigner(t)
	asc := clearsignText(t, stranger, "abc  node-v24.1.0-linux-x64.tar.gz\n")
	if _, err := verifySums(asc, keys); err == nil || !strings.Contains(err.Error(), "not signed by a Node.js release key") {
		t.Fatalf("stranger's signature: %v", err)
	}
	if _, err := verifySums([]byte("abc  node.tar.gz\n"), keys); err == nil {
		t.Fatal("an unsigned file verified")
	}
	// With the stranger's key pinned instead, the same file verifies.
	if _, err := verifySums(asc, openpgp.EntityList{stranger}); err != nil {
		t.Fatal(err)
	}
}

// testSigner makes a fresh key, a stand-in for a Node releaser's.
func testSigner(t *testing.T) *openpgp.Entity {
	t.Helper()
	e, err := openpgp.NewEntity("test releaser", "", "test@example.invalid", &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func clearsignText(t *testing.T, signer *openpgp.Entity, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, signer.PrivateKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(text))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
