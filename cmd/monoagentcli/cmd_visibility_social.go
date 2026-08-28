//go:build social

package main

// hideLegacySocialCommands returns nil in the social build: every legacy
// social verb stays visible in the default help because this build was
// explicitly compiled for those platforms.
func hideLegacySocialCommands() []string {
	return nil
}
