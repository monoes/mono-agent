package extension

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/capture"
)

// A capture written while locked is stored, and nothing runs on it: the
// after-write hook (the bridge's summary, page-kind classifier and indexing)
// is skipped. In every other state it runs once per capture.
func TestTheAfterWriteHookIsSkippedWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			srv, ext, inbox := startCaptureServer(t)
			var hooked atomic.Int32
			srv.SetAfterWrite(func(*capture.Result) { hooked.Add(1) })
			landed := make(chan error, 1)
			srv.OnCapture(func(_ *capture.Result, err error) { landed <- err })
			accounttest.Install(t, c.Mode)

			ext.sendFinal("ext-pushed-1", sampleMeta(), b64Artifact(capture.ArtifactReadable, "# A Post"))
			select {
			case err := <-landed:
				if err != nil {
					t.Fatalf("the pushed capture was not written: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the pushed capture never landed")
			}
			if entries, err := capture.List(inbox); err != nil || len(entries) != 1 {
				t.Fatalf("inbox = %+v, %v, want the pushed capture", entries, err)
			}
			// OnCapture reports a pushed capture after its hook has returned, so
			// the count is final.
			want := int32(1)
			if c.Refused {
				want = 0
			}
			if got := hooked.Load(); got != want {
				t.Errorf("the after-write hook ran %d times, want %d", got, want)
			}
		})
	}
}
