package main

import (
	"bufio"
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/agentinstall"
	"github.com/monoes/mono-agent/internal/monomind"
)

func newAgentInstallCmd(cfg *globalConfig) *cobra.Command {
	var yes, force bool
	cmd := &cobra.Command{
		Use:   "install <runtime>",
		Short: "Install an AI agent runtime (claude, codex, opencode, …)",
		Long: `Installs one of the runtimes ` + "`agent scan`" + ` lists, using the install
recipe monomind reports for it:

  npm packages      npm install -g, through a suitable system Node or the
                    managed one (monoagentcli nodejs install); into
                    ~/.monoagent/npm-global when the system prefix needs root
  vendor installer  the vendor's https install script, downloaded and run;
                    asks first (or pass --yes)
  anything else     printed as manual instructions

With --json, progress is streamed as NDJSON like ` + "`doctor fix`" + `.`,
		Example: `  monoagentcli agent install claude
  monoagentcli agent install antigravity --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			progress := progressWriter(out, cfg.JSONOutput)
			confirmScript := func(r agentinstall.Recipe) bool {
				if yes {
					return true
				}
				if cfg.JSONOutput || !stdinIsTerminal() {
					return false
				}
				fmt.Fprintf(out, "Run the vendor installer %s with %s? [y/N] ", r.ScriptURL, r.Shell)
				ans, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				a := strings.ToLower(strings.TrimSpace(ans))
				return a == "y" || a == "yes"
			}
			msg, err := installRuntime(cmd.Context(), args[0], force, confirmScript, progress)
			return finishStreamed(out, cfg.JSONOutput, err, msg)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Run vendor install scripts without asking")
	cmd.Flags().BoolVar(&force, "force", false, "Install even if the runtime is already installed (upgrade)")
	return cmd
}

// installRuntime installs one runtime by its `agent scan` id and verifies
// it with a re-scan. confirmScript gates vendor install scripts.
func installRuntime(ctx context.Context, id string, force bool, confirmScript func(agentinstall.Recipe) bool,
	progress func(string)) (string, error) {
	scan, err := monomind.Scan(ctx)
	if err != nil {
		return "", err
	}
	entry := scan.Find(id)
	if entry == nil {
		ids := make([]string, 0, len(scan.Agents))
		for _, a := range scan.Agents {
			ids = append(ids, a.ID)
		}
		sort.Strings(ids)
		return "", errNotFound("unknown runtime %q (known: %s)", id, strings.Join(ids, ", "))
	}
	if entry.Installed && !force {
		return fmt.Sprintf("%s is already installed (%s) — use --force to reinstall", id, strPtr(entry.Version, "unknown version")), nil
	}

	recipe := agentinstall.Parse(entry.InstallHint)
	switch recipe.Kind {
	case agentinstall.KindManual:
		return "", errInvalidInput("%s can't be installed automatically — %s", id, entry.InstallHint)
	case agentinstall.KindScript:
		if !confirmScript(recipe) {
			return "", errInvalidInput("%s installs by running the vendor script %s — confirm, or pass --yes", id, recipe.ScriptURL)
		}
	}
	if err := agentinstall.New().Install(ctx, recipe, progress); err != nil {
		return "", err
	}

	after, err := monomind.Scan(ctx)
	if err != nil {
		return "", fmt.Errorf("installed, but re-scanning failed: %w", err)
	}
	got := after.Find(id)
	if got == nil || !got.Installed {
		return "", fmt.Errorf("the installer finished but monomind still doesn't see %s — open a new terminal, or check the output above", id)
	}
	bin := strPtr(got.Binary, id)
	progress(fmt.Sprintf("sign in: run `%s` once in a terminal if it asks you to log in", filepath.Base(bin)))
	return fmt.Sprintf("%s %s installed at %s", id, strPtr(got.Version, ""), bin), nil
}

func strPtr(s *string, def string) string {
	if s == nil || *s == "" {
		return def
	}
	return *s
}
