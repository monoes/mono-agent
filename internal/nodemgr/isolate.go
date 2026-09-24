package nodemgr

import (
	"os/exec"
	"time"

	"github.com/monoes/mono-agent/internal/proctree"
)

// isolate makes an install command safe to cancel and unable to wait on a
// person. It runs in its own session on unix (no controlling terminal: an
// installer that asks on /dev/tty, or sudo waiting for a password, fails
// at once instead of hanging), and cancelling its context kills the whole
// tree it started, not only the direct child. WaitDelay stops a descendant
// that keeps the output pipe open from holding the call forever.
func isolate(cmd *exec.Cmd) {
	detach(cmd)
	cmd.Cancel = func() error {
		proctree.Kill(cmd)
		return nil
	}
	cmd.WaitDelay = 10 * time.Second
}
