package proctree

import (
	"os/exec"
	"testing"
)

func TestKillUnstartedIsNoop(t *testing.T) {
	Kill(nil)
	Kill(exec.Command("x")) // must not panic or signal any group
}
