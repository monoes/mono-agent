package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/autostart"
)

// newDaemonRestartCmd restarts the daemon through the OS service it is registered as, so that
// it reads the settings saved with `api config` (see `api config show` for what a running
// daemon started with). It opens no database.
func newDaemonRestartCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon through the auto-start service it is registered as",
		Long: "Restarts the daemon through the service manager it is registered with (a LaunchAgent on macOS, a systemd --user unit " +
			"on Linux, a Scheduled Task on Windows; see `daemon install`), so that it reads the settings saved with " +
			"`monoagentcli api config`. It interrupts whatever the daemon is running (workflows, org runs). A daemon that is not " +
			"registered for auto-start cannot be restarted by this command (exit 3): stop it and start `monoagentcli daemon` again " +
			"by hand. If a daemon was started by hand while the service is registered, stop it first: the service's daemon would " +
			"find the home taken and exit.",
		Example: "  monoagentcli daemon restart\n  monoagentcli daemon restart --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runDaemonRestart(ctx, cmd, cfg)
		},
	}
}

func runDaemonRestart(ctx context.Context, cmd *cobra.Command, cfg *globalConfig) error {
	fmt.Fprintln(cmd.ErrOrStderr(), "Restarting the daemon interrupts whatever it is running (workflows, org runs).")
	res, err := autostart.RestartRegistered(ctx, newInstaller())
	if err != nil {
		var notRegistered *autostart.NotRegisteredError
		if errors.As(err, &notRegistered) {
			return errInvalidInput("%v", err)
		}
		return fmt.Errorf("restart: %w", err)
	}
	if cfg.JSONOutput {
		return writeJSONTo(cmd.OutOrStdout(), res)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Restarted the daemon through %s. What it was running (workflows, org runs) was interrupted; `monoagentcli api config show` shows what it started with.\n", res.Via)
	return nil
}
