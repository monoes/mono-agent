package account

import "sync"

// globalsMu guards every process-wide variable of the package: the
// enforcement date (rollout.go), the key override (keys.go), the strict flag
// (below) and the installed guard (process.go). They are read only through
// accessors that take the lock. Tests change them only through the *ForTest
// hooks (testhooks.go), which take it too; a test that uses them must not call
// t.Parallel().
var globalsMu sync.RWMutex

// strict turns off the test-binary exception of Require (process.go).
var strict bool
