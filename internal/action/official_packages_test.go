package action

import "github.com/monoes/mono-agent/automations"

// The app no longer embeds the official automation packages; tests that load
// their actions without a registry read them from the repo's automations/.
func init() { SetTestFallback(automations.FS()) }
