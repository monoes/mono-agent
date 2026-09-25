//go:build !nosocial

package main

// hideLegacySocialCommands returns nil in the default (social) build: every legacy
// social verb stays visible in the default help because this build was
// explicitly compiled for those platforms.
func hideLegacySocialCommands() []string {
	return nil
}
