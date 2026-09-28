package workflow

import (
	"github.com/monoes/mono-agent/automations"
	"github.com/monoes/mono-agent/internal/action"
)

// The app no longer embeds the official automation packages; tests that load
// their actions without a registry read them from the repo's automations/.
func init() { action.SetTestFallback(automations.FS()) }
