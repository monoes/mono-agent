package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// The CLI gate (spec §6.1, D6, D20): layer 1 of the monoes.me account gate.
// Every command carries a class in Annotations[accountAnnotation]:
//
//	open   runs whatever the account state;
//	gated  with no valid session is refused before cobra runs a single hook
//	       (no first-run check, no database open; from the enforcement date the
//	       guard writes only the clock-guard record, A25): exit 4;
//	serve  a long-running command (spec §6.4): it starts even when locked and
//	       refuses the work itself (layers 2 and 3), because a daemon that
//	       exited on the lock would be respawned in a loop by launchd, and a
//	       locked MCP server or bridge could not tell its client what to do.
//
// One table classifies the whole command tree, so the commands need no edit
// and a command added later is gated until someone opens it here.
const (
	accountAnnotation = "monoagent.account"
	classOpen         = "open"
	classGated        = "gated"
	classServe        = "serve"
)

// accountClasses is that table, keyed by command path without the root's
// name. A command takes the class of its nearest listed ancestor: `ref` covers
// every `ref` subcommand and `account` all the account commands. A command
// that nothing lists is gated, and so is one stamped with any other word.
//
// `org serve` is not a serving command here: it is a launcher that starts the
// external monomind process and returns, so a refusal causes no respawn loop and
// a locked machine does not start org services (D8).
//
// help, completion and __complete are cobra's own: it adds them while a
// command executes, after the gate has looked, so they reach the gate as the
// root with leftover arguments (see invocationClass) and are listed here for
// the inventory test. __completeNoDesc is an alias of __complete.
var accountClasses = map[string]string{
	"version":        classOpen,
	"ref":            classOpen,
	"update":         classOpen,
	"doctor":         classOpen,
	"doctor fix":     classOpen,
	"setup":          classOpen,
	"release":        classOpen,
	"account":        classOpen,
	"library login":  classOpen,
	"library logout": classOpen,
	"library status": classOpen,
	"help":           classOpen,
	"completion":     classOpen,
	"__complete":     classOpen,

	"daemon":           classServe,
	"daemon install":   classGated,
	"daemon restart":   classGated,
	"daemon uninstall": classGated,
	"httpapi":          classServe,
	"mcp":              classServe,
	"extension serve":  classServe,
}

// commandKey is c's path below the root: "doctor fix", "library login".
func commandKey(c *cobra.Command) string {
	var names []string
	for ; c != nil && c.HasParent(); c = c.Parent() {
		names = append([]string{c.Name()}, names...)
	}
	return strings.Join(names, " ")
}

// applyClassification stamps every command below root with its class from
// accountClasses. It can run again on a tree that gained commands.
func applyClassification(root *cobra.Command) {
	var walk func(parent *cobra.Command, inherited string)
	walk = func(parent *cobra.Command, inherited string) {
		for _, c := range parent.Commands() {
			class := inherited
			if listed, ok := accountClasses[commandKey(c)]; ok {
				class = listed
			}
			if class == "" {
				class = classGated
			}
			if c.Annotations == nil {
				c.Annotations = map[string]string{}
			}
			c.Annotations[accountAnnotation] = class
			walk(c, class)
		}
	}
	walk(root, "")
}

// commandClass is c's own stamp, else its nearest stamped ancestor's, else
// gated; a stamp that is not open or serve counts as gated. The root is never
// consulted: invocationClass decides what the root itself may do.
func commandClass(c *cobra.Command) string {
	for ; c != nil && c.HasParent(); c = c.Parent() {
		if class, ok := c.Annotations[accountAnnotation]; ok {
			if class == classOpen || class == classServe {
				return class
			}
			return classGated
		}
	}
	return classGated
}
