package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func refAccountCmd() *cobra.Command {
	return &cobra.Command{Use: "account", Short: "The monoes.me account: optional today, how to sign in, headless and Docker", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), `The monoes.me account

Status today: a monoes.me account is optional. It will be required from a date
announced at monoes.me/mono-agent; until then nothing is locked, no command warns
and no command contacts monoes.me unless you run "account" or "library" yourself.
Releases are published on monoes.me.

One sign-in per machine, shared by every profile:
  monoagentcli account login                      # opens the browser
  monoagentcli account login --email you@x.com    # a code by email, for a machine without a browser
  monoagentcli account status [--offline]         # who is logged in, and until when
  monoagentcli account logout                     # revoke and forget the session

Headless machines and Docker containers:
  Use the email-code flow. "--send" only sends the code and "--code" finishes the sign-in:
    monoagentcli account login --email you@x.com --send
    monoagentcli account login --email you@x.com --code 123456
  In a container, run these inside the running container (for example
  "docker compose exec monoagent monoagentcli account login --email you@x.com"),
  so the session is written to the container's /data volume. Do not copy
  ~/.monoagent/account between machines or bake it into an image. The refresh token
  is sealed with the system key store; where there is none (Alpine images), set
  MONOAGENT_ALLOW_FILE_KEYRING=1.

"account status" exits 0 while the session is good (including offline grace) and 4 when it is not.
Add --json for machine-readable output.`)
	}}
}
