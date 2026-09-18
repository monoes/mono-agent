package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// newOrgQueuedCmd lists messages waiting in an org's offline queue (C-35).
// monomind drains `inbox.jsonl` only when the org starts, so a message sent
// to a stopped org — including an automation role's reply — sits there until
// then. This reads the queue without monomind and never writes to it.
func newOrgQueuedCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "queued <org>",
		Short: "List messages queued for an org's next start (read-only)",
		Long: "Lists the messages monomind queued for an org that was not running when they were sent " +
			"(<folder>/.monomind/orgs/<org>/inbox.jsonl). They are delivered when the org next starts. " +
			"The queue file is only read, never changed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			org := args[0]
			if !orgdesign.ValidOrgName(org) {
				return errInvalidInput("invalid org name %q", org)
			}
			root := env.Root()
			inbox, err := orgbridge.InboxPath(root, org)
			if err != nil {
				return errInvalidInput("%s", err.Error())
			}
			if !orgKnown(root, org, inbox) {
				return errNotFound("org %q not found under %s", org, orgdesign.OrgsDir(root))
			}
			msgs, skipped, err := orgbridge.ReadQueued(root, org)
			if err != nil {
				return err
			}
			return printJSONValue(map[string]interface{}{
				"v": 1, "org": org, "count": len(msgs), "skipped": skipped,
				"delivery": "at next start", "messages": msgs,
			})
		},
	}
}

// orgKnown reports whether org has a config file or a queue of its own. A
// queue without a config (a deleted org that still received messages) is
// listed rather than hidden.
func orgKnown(root, org, inbox string) bool {
	for _, p := range []string{filepath.Join(orgdesign.OrgsDir(root), org+".json"), inbox, inbox + ".draining"} {
		if _, err := os.Stat(p); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return true
		}
	}
	return false
}
