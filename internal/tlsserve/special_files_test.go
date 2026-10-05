package tlsserve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The paths of the saved settings can be changed by a caller that is not the operator (an MCP host
// with --allow-mutations), so a path that names something that is not a certificate must be an
// error that the server reports, never a read that does not end or a start that waits for ever.

// resolveWithin runs Resolve and fails the test, without waiting for it, when it has not returned
// within the time: a read that never ends must be a failure of the test and not a hung suite.
func resolveWithin(t *testing.T, c Config, within time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := Resolve(c)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("Resolve did not return within %v: it is reading something that does not end", within)
		return nil
	}
}

func TestAFileThatIsLargerThanAnyCertificateIsNotRead(t *testing.T) {
	noEnvironmentPair(t)
	dir := t.TempDir()
	_, keyPath, _ := writePair(t, dir, "ok")
	big := filepath.Join(dir, "big.pem")
	if err := os.WriteFile(big, []byte(strings.Repeat("A", 2<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	c := testConfig("0.0.0.0:0")
	c.CertFile, c.KeyFile = big, keyPath
	err := resolveWithin(t, c, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("a 2 MiB certificate file: %v, want an error that says it is too large", err)
	}
}

// A certificate chain with its key is far below the limit: a file just inside it is read as before.
func TestACertificateFileJustInsideTheLimitStillLoads(t *testing.T) {
	noEnvironmentPair(t)
	dir := t.TempDir()
	certPath, keyPath, der := writePair(t, dir, "chain")
	// Padding after the PEM block is text that a PEM decoder skips: a long chain looks like this.
	pem, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	padded := append(pem, []byte(strings.Repeat("# a comment\n", 60000))...) // about 720 KiB
	if len(padded) >= 1<<20 {
		t.Fatalf("the test file is %d bytes, which is not inside the limit", len(padded))
	}
	if err := os.WriteFile(certPath, padded, 0o644); err != nil {
		t.Fatal(err)
	}
	c := testConfig("0.0.0.0:0")
	c.CertFile, c.KeyFile = certPath, keyPath
	cfg, err := Resolve(c)
	if err != nil || cfg == nil || len(cfg.Certificates) == 0 || string(cfg.Certificates[0].Certificate[0]) != string(der) {
		t.Errorf("a certificate file of %d bytes: cfg=%v err=%v", len(padded), cfg, err)
	}
}

func TestADirectoryIsNotACertificateFile(t *testing.T) {
	noEnvironmentPair(t)
	dir := t.TempDir()
	_, keyPath, _ := writePair(t, dir, "ok")
	c := testConfig("0.0.0.0:0")
	c.CertFile, c.KeyFile = dir, keyPath
	err := resolveWithin(t, c, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a folder as the certificate file: %v, want an error that says it is not a regular file", err)
	}
}

// The environment pair is read the same way: nothing about this is specific to the saved settings,
// except who can change them.
func TestTheEnvironmentPairIsHeldToTheSameRule(t *testing.T) {
	noEnvironmentPair(t)
	dir := t.TempDir()
	_, keyPath, _ := writePair(t, dir, "ok")
	t.Setenv(testCertEnv, dir)
	t.Setenv(testKeyEnv, keyPath)
	err := resolveWithin(t, testConfig("0.0.0.0:0"), 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a folder as the certificate in the environment: %v", err)
	}
}
