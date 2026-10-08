package main

import (
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// invocationClass is the class of what running args would execute.
//
// It resolves the target with root.Find on the same tree cobra executes, so
// the answer is the command cobra will run (spike S5, spec §12). Three things
// the plain Find does not say:
//
//   - help, completion, __complete and __completeNoDesc exist only once
//     cobra's ExecuteC has added them, so here they resolve to the root with
//     the arguments left over. So does an unknown command, and a bare root.
//     Cobra prints help or an error for the root and runs nothing: open.
//   - Find runs before the help flag exists and takes `--help` for a flag that
//     wants a value, so whether cobra prints help instead of running the
//     command needs the flags parsed: asksForHelp.
//   - After `--` everything is an argument, and a value flag takes the next
//     word whatever it looks like (`--profile doctor workflow list` is
//     `workflow list`): Find already does both.
func invocationClass(root *cobra.Command, args []string) string {
	target, _, err := root.Find(args)
	if err != nil || target == nil || target == root {
		return classOpen
	}
	class := commandClass(target)
	if class == classOpen || asksForHelp(args) {
		return classOpen
	}
	return class
}

// asksForHelp reports whether cobra will print help for args instead of
// running the command: the help flag parses to true for the resolved command.
// Only the flags decide, so they are parsed the way cobra will (which words
// are flag values, what `--` ends, `--help=false`) on a throwaway tree:
// parsing them on the real one would be repeated by the real run, and a
// repeated string-slice flag would collect its values twice.
func asksForHelp(args []string) bool {
	if !mayAskForHelp(args) {
		return false
	}
	probe := newRootCmd()
	probe.SetOut(io.Discard)
	probe.SetErr(io.Discard)
	cmd, flags, err := probe.Find(args)
	if err != nil || cmd == nil {
		return false
	}
	cmd.InitDefaultHelpFlag()
	if cmd.ParseFlags(flags) != nil {
		return false
	}
	help, err := cmd.Flags().GetBool("help")
	return err == nil && help
}

// mayAskForHelp is the cheap test before the throwaway tree: some word could
// be the help flag (`--help`, `--help=x`, or a short group holding h, as in
// `-h` and `-vh`). It over-approximates; asksForHelp decides.
func mayAskForHelp(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--help" || strings.HasPrefix(a, "--help="):
			return true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "h"):
			return true
		}
	}
	return false
}
