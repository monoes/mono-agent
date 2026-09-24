// Package proctree kills a child process together with everything it
// spawned. It knows nothing about monomind: internal/monomind uses it for
// the children it runs, and the Wails GUI uses it for its chat and org
// subprocesses without importing internal/monomind (the GUI reaches
// monomind only through monoagentcli).
package proctree
