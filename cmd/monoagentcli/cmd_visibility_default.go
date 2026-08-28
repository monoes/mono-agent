//go:build !social

package main

// hideLegacySocialCommands lists legacy social-oriented top-level commands
// hidden from the DEFAULT build's help output. Hidden commands remain
// fully invokable by name (cobra only skips them in help/completion
// listings); the compiled-in behavior is unchanged, so existing scripts
// keep working. login/crawl/connect/people are intentionally NOT hidden.
func hideLegacySocialCommands() []string {
	return []string{"message", "comment", "search", "list", "template"}
}
