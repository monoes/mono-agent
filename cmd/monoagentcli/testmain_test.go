package main

import (
	"testing"

	"github.com/monoes/mono-agent/automations"
	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/testhome"
)

// Tests never touch the real ~/.monoagent. The app no longer ships the
// official packages; tests get them from the repo's automations/ source,
// seeded into each test home and readable without a registry.
func TestMain(m *testing.M) {
	automation.TestSeed = automations.FS()
	action.SetTestFallback(automations.FS())
	testhome.Main(m)
}
