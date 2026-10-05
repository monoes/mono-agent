//go:build !windows

package tlsserve

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opening a FIFO that nothing writes to waits for ever, and a device such as /dev/zero never ends:
// the two files that the review of phase 6 named. A FIFO is made here; /dev/null stands for a
// device that is safe to read in the run that shows the test failing before the fix.

func TestAFifoIsNotACertificateFileAndIsNeverWaitedFor(t *testing.T) {
	noEnvironmentPair(t)
	dir := t.TempDir()
	_, keyPath, _ := writePair(t, dir, "ok")
	fifo := dir + "/cert.fifo"
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	for name, c := range map[string]Config{"as the certificate": {CertFile: fifo, KeyFile: keyPath}, "as the key": {CertFile: keyPath, KeyFile: fifo}} {
		c.Addr, c.CertEnv, c.KeyEnv = "0.0.0.0:0", testCertEnv, testKeyEnv
		c.CacheDir, c.CommonName, c.Label = "tlsserve-test", "tlsserve test", "test server"
		err := resolveWithin(t, c, 5*time.Second)
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("a FIFO %s: %v, want an error that says it is not a regular file", name, err)
		}
	}
}

func TestADeviceIsNotACertificateFile(t *testing.T) {
	noEnvironmentPair(t)
	_, keyPath, _ := writePair(t, t.TempDir(), "ok")
	c := testConfig("0.0.0.0:0")
	c.CertFile, c.KeyFile = "/dev/null", keyPath
	err := resolveWithin(t, c, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("/dev/null as the certificate: %v, want an error that says it is not a regular file", err)
	}
}
