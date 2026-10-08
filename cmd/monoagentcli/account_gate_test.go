package main

import (
	"slices"
	"sort"
	"testing"

	"github.com/spf13/cobra"
)

// pinnedOpen and pinnedServe are the open list of D6 and the serving commands
// of spec §6.4, spelled out per command path. Opening a command is a decision,
// so it takes an edit here too, in the review that opens it. `account` is all
// of the account commands, `ref` all the reference pages; help and completion
// are cobra's own. __complete is cobra's as well, but exists only while it
// executes, so TestOpenCommandsStayOpenWhenLocked covers it.
var (
	pinnedOpen = []string{
		"account", "account login", "account logout", "account status",
		"completion", "completion bash", "completion fish", "completion powershell", "completion zsh",
		"doctor", "doctor fix",
		"help",
		"library login", "library logout", "library status",
		"ref", "ref api", "ref commands", "ref connections", "ref crawling", "ref examples", "ref expressions",
		"ref node", "ref nodes", "ref org", "ref publication", "ref tasks", "ref templates", "ref workflow",
		"setup", "update", "version",
	}
	pinnedServe = []string{"daemon", "extension serve", "httpapi", "mcp"}
)

// allCommands lists every command below root, parents first.
func allCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			out = append(out, sub)
			walk(sub)
		}
	}
	walk(root)
	return out
}

// Every command is stamped, and the open and serving sets are exactly the
// pinned lists: the idiom of TestEveryRegisteredNodeTypeIsClassified.
func TestEveryCommandIsClassified(t *testing.T) {
	root := newRootCmd()
	// Cobra adds help and completion while a command executes; add them so
	// the inventory sees them.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	applyClassification(root)

	all := allCommands(root)
	var open, serve []string
	for _, c := range all {
		class, stamped := c.Annotations[accountAnnotation]
		if !stamped || (class != classOpen && class != classGated && class != classServe) {
			t.Errorf("%q has class %q", commandKey(c), class)
		}
		switch commandClass(c) {
		case classOpen:
			open = append(open, commandKey(c))
		case classServe:
			serve = append(serve, commandKey(c))
		}
	}
	t.Logf("%d commands classified: %d open, %d serving", len(all), len(open), len(serve))

	for name, pair := range map[string][2][]string{"open": {pinnedOpen, open}, "serve": {pinnedServe, serve}} {
		want := append([]string(nil), pair[0]...)
		sort.Strings(want)
		sort.Strings(pair[1])
		if !slices.Equal(want, pair[1]) {
			t.Errorf("the %s commands differ from the pinned list (D6)\nwant %q\n got %q", name, want, pair[1])
		}
	}
}

// A table entry that names no command would silently classify nothing.
func TestAccountClassesNameRealCommands(t *testing.T) {
	root := newRootCmd()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	known := map[string]bool{"__complete": true} // cobra adds it only while it runs it
	for _, c := range allCommands(root) {
		known[commandKey(c)] = true
	}
	for key := range accountClasses {
		if !known[key] {
			t.Errorf("accountClasses lists %q, which is not a command", key)
		}
	}
}

// Default-deny at run time: a command added after the classification, or
// stamped with anything but the exact words open and serve, is gated; a child
// of an open command takes its parent's class, which pinnedOpen then makes visible.
func TestUnlistedCommandsAreGated(t *testing.T) {
	root := newRootCmd()
	applyClassification(root)
	noop := func(*cobra.Command, []string) error { return nil }
	brandNew := &cobra.Command{Use: "brand-new", RunE: noop}
	mistyped := &cobra.Command{Use: "mistyped", RunE: noop, Annotations: map[string]string{accountAnnotation: "opne"}}
	root.AddCommand(brandNew, mistyped)
	for _, c := range []*cobra.Command{brandNew, mistyped} {
		if got := commandClass(c); got != classGated {
			t.Errorf("%s: class %q, want gated", c.Name(), got)
		}
	}
	ref, _, err := root.Find([]string{"ref"})
	if err != nil {
		t.Fatal(err)
	}
	child := &cobra.Command{Use: "brand-new-page", RunE: noop}
	ref.AddCommand(child)
	if commandClass(child) != classOpen {
		t.Error("a child of an open command should inherit open")
	}
}
