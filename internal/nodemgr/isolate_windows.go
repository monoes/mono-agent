//go:build windows

package nodemgr

import "os/exec"

// detach is a no-op on Windows: proctree.Kill ends the tree with taskkill.
func detach(*exec.Cmd) {}
