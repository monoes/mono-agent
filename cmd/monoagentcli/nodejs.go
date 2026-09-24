package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/nodemgr"
)

// `node` is taken by the workflow node runner, hence `nodejs`.
func newNodejsCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodejs",
		Short: "Manage the private Node.js runtime monoagent can install for monomind",
		Long: `monomind and the npm-based AI agent CLIs need Node.js >= ` + nodemgr.MinVersion + `.
When the machine has no suitable Node, monoagent can download one into
~/.monoagent/node (official nodejs.org build over HTTPS, checked against nodejs.org's SHASUMS256). It is only
used by processes monoagent starts and never touches your shell config; a
suitable system Node always takes precedence.`,
	}
	cmd.AddCommand(newNodejsStatusCmd(cfg), newNodejsInstallCmd(cfg), newNodejsUpdateCmd(cfg), newNodejsRemoveCmd(cfg))
	return cmd
}

type nodejsStatus struct {
	MinVersion     string   `json:"min_version"`
	SystemPath     string   `json:"system_path,omitempty"`
	SystemVersion  string   `json:"system_version,omitempty"`
	SystemSuitable bool     `json:"system_suitable"`
	Managed        string   `json:"managed_version,omitempty"`
	ManagedPath    string   `json:"managed_path,omitempty"`
	Installed      []string `json:"installed"`
	Active         string   `json:"active"` // "system" | "managed" | "none"
}

func newNodejsStatusCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which Node.js monoagent uses",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m := nodemgr.New()
			st := nodejsStatus{MinVersion: nodemgr.MinVersion, Installed: []string{}, Active: "none"}
			if p, v, ok := m.SystemNode(cmd.Context()); ok {
				st.SystemPath, st.SystemVersion, st.SystemSuitable = p, v, nodemgr.Suitable(v)
			}
			if inst, err := m.Installed(); err == nil && inst != nil {
				st.Installed = inst
			}
			if v, ok := m.Current(); ok {
				st.Managed, st.ManagedPath = v, m.NodePath(v)
			}
			switch {
			case st.SystemSuitable:
				st.Active = "system"
			case st.Managed != "":
				st.Active = "managed"
			}

			out := cmd.OutOrStdout()
			if cfg.JSONOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(st)
			}
			sys := "not found"
			if st.SystemPath != "" {
				sys = fmt.Sprintf("v%s (%s)", st.SystemVersion, st.SystemPath)
				if !st.SystemSuitable {
					sys += " — too old, need >= " + nodemgr.MinVersion
				}
			}
			managed := "not installed"
			if st.Managed != "" {
				managed = fmt.Sprintf("v%s (%s)", st.Managed, st.ManagedPath)
			}
			fmt.Fprintf(out, "system Node:  %s\nmanaged Node: %s\nin use:       %s\n", sys, managed, st.Active)
			if st.Active == "none" {
				fmt.Fprintln(out, "\nInstall one with: monoagentcli nodejs install")
			}
			return nil
		},
	}
}

func newNodejsInstallCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "install [version]",
		Short: "Download and activate a managed Node.js (default: latest LTS)",
		Long: `Downloads Node.js from nodejs.org into ~/.monoagent/node, verifies its
SHA-256 checksum, and makes it the active managed version. [version] is
"lts" (default), a major ("24") or an exact version ("24.1.0").
With --json, progress is streamed as NDJSON like ` + "`doctor fix`" + `.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			want := ""
			if len(args) == 1 {
				want = args[0]
			}
			return runNodejsInstall(cmd, cfg, want, false)
		},
	}
}

func newNodejsUpdateCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Move the managed Node.js to the latest LTS and remove older versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, ok := nodemgr.New().Current(); !ok {
				return errNotFound("no managed Node.js installed — use: monoagentcli nodejs install")
			}
			return runNodejsInstall(cmd, cfg, "lts", true)
		},
	}
}

func runNodejsInstall(cmd *cobra.Command, cfg *globalConfig, want string, prune bool) error {
	out := cmd.OutOrStdout()
	progress := progressWriter(out, cfg.JSONOutput)
	m := nodemgr.New()
	v, err := m.Install(cmd.Context(), want, progress)
	if err == nil && prune {
		if perr := m.Prune(v); perr != nil {
			progress("could not remove older versions: " + perr.Error())
		}
	}
	return finishStreamed(out, cfg.JSONOutput, err, "managed Node.js v"+v+" is active")
}

// finishStreamed ends a command whose progress was streamed with
// progressWriter: a final NDJSON done/error event in JSON mode, a summary
// line otherwise.
func finishStreamed(out io.Writer, asJSON bool, err error, okMsg string) error {
	if asJSON {
		if err != nil {
			writeFixEvent(out, fixEvent{Kind: "error", Message: err.Error()})
			return &cliError{code: 1, msg: err.Error()}
		}
		writeFixEvent(out, fixEvent{Kind: "done"})
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ %s\n", okMsg)
	return nil
}

func newNodejsRemoveCmd(cfg *globalConfig) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove [version]",
		Short: "Remove the managed Node.js (one version, or everything incl. packages installed with it)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m := nodemgr.New()
			version := ""
			what := "the managed Node.js and every package installed with it (" + m.Root + ", " + m.NpmRoot + ")"
			if len(args) == 1 {
				version = args[0]
				what = "managed Node.js " + version
			}
			if !yes {
				if cfg.JSONOutput || !stdinIsTerminal() {
					return errInvalidInput("refusing to remove %s without --yes", what)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Remove %s? [y/N] ", what)
				ans, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
					return nil
				}
			}
			if err := m.Remove(version); err != nil {
				return err
			}
			if cfg.JSONOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"removed": what})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ removed %s\n", what)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Don't ask for confirmation")
	return cmd
}
