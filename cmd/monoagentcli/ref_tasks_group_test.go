package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// The helpers of the tests that hold the texts and the gate table to the commands of the task group.
// A command of the group is named by its path under task: "add" or, in a group of its own, "os install".

// refTaskSub finds `task NAME` the way the CLI does, in a root command whose global
// flags its subcommands inherit. NAME is a command of the group, or the path to one in a
// group of its own ("os install"); nil for a name that is no command.
func refTaskSub(name string) *cobra.Command {
	words := strings.Fields(name)
	if len(words) == 0 {
		return nil
	}
	sub, rest, err := newRootCmd().Find(append([]string{"task"}, words...))
	if err != nil || len(rest) != 0 || sub.Name() != words[len(words)-1] {
		return nil
	}
	return sub
}

// refTaskCommands are the commands of the task group by their path under task: "add", "os"
// and, below a group, "os install".
func refTaskCommands() map[string]*cobra.Command {
	found := map[string]*cobra.Command{}
	var walk func(prefix string, group *cobra.Command)
	walk = func(prefix string, group *cobra.Command) {
		for _, sub := range group.Commands() {
			found[prefix+sub.Name()] = sub
			walk(prefix+sub.Name()+" ", sub)
		}
	}
	walk("", newTaskCmd(&globalConfig{}))
	return found
}

// refTaskCallee reads a call that refTaskCall found in a text: the command it calls, as its
// path under task ("add", "os install"), and what is left of the line after it, which holds
// the call's flags. A group takes the word after it for its subcommand when that is one, so
// `task os install --force` calls install: the command tree says which commands are groups.
// The command is nil when the call names none.
func refTaskCallee(call []string) (path string, sub *cobra.Command, rest string) {
	path, rest = call[1], call[2]
	if sub = refTaskSub(path); sub == nil {
		return path, nil, rest
	}
	for sub.HasSubCommands() {
		word, tail, _ := strings.Cut(strings.TrimSpace(rest), " ")
		next := refTaskSub(path + " " + word)
		if word == "" || next == nil {
			break
		}
		path, sub, rest = path+" "+word, next, tail
	}
	return path, sub, rest
}

// refHasFlag says whether a command declares the flag, as its own or, for a group, as one
// of all its commands (os has --dest), or inherits it.
func refHasFlag(sub *cobra.Command, name string) bool {
	return sub.Flags().Lookup(name) != nil || sub.PersistentFlags().Lookup(name) != nil || sub.InheritedFlags().Lookup(name) != nil
}
