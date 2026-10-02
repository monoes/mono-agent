package main

import (
	"fmt"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/openaiapi"
)

// The document `api models --json` prints is the one the MCP tool api_models_list
// returns, so its types and the rules that fill them live in internal/openaiapi.
type (
	apiAutoJSON   = openaiapi.AutoReport
	apiModelsJSON = openaiapi.ModelsReport
)

// autoNote is the line the text output gives for it.
func autoNote(a apiAutoJSON) string {
	if !a.Available {
		return "off, it needs " + a.Missing
	}
	note := "available"
	if a.Candidates > 0 {
		plural := "s"
		if a.Candidates == 1 {
			plural = ""
		}
		note += fmt.Sprintf(" (Jev picks among the %d model%s it may use here, up to %s", a.Candidates, plural, a.Confinement)
		if a.HeldBack > 0 {
			note += fmt.Sprintf("; %d more are served here but above --auto-confinement", a.HeldBack)
		}
		note += ")"
	}
	if a.KeySource == "env" {
		note += "; the Jev key is this shell's TYPESAFE_API_KEY, and a running server reads its own environment"
	}
	return note
}

func newAPIModelsCmd(cfg *globalConfig) *cobra.Command {
	var forListener, confinement, contextConfinement, autoConfinement string
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List the models /v1/models would serve, with each one's confinement class",
		Long: "Lists every model of the installed agent runtimes with its confinement class (chat-only, sandboxed or " +
			"unconfined) and whether the confinement policy of a loopback or a network listener allows it. " +
			"The policy is --confinement, else MONOAGENT_API_CONFINEMENT, else the listener's default: any on " +
			"loopback, chat-only on a network bind. A key created with --context is held to --context-confinement, " +
			"else MONOAGENT_API_CONTEXT_CONFINEMENT, else chat-only, and never above the listener's policy. " +
			"The auto model is held to --auto-confinement, else MONOAGENT_API_AUTO_CONFINEMENT, else chat-only, the same way. " +
			"IMAGES (capabilities in --json) says which models make images, from MONOAGENT_API_IMAGE_RUNTIMES, else codex and antigravity.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := representativeAddr(forListener)
			if err != nil {
				return err
			}
			policy, err := effectivePolicy(addr, confinement, os.Getenv)
			if err != nil {
				return err
			}
			if policy.ContextMax, err = effectiveContextMax(contextConfinement, os.Getenv); err != nil {
				return err
			}
			if policy.AutoMax, err = effectiveAutoMax(autoConfinement, os.Getenv); err != nil {
				return err
			}
			imageRuntimes, err := openaiapi.EffectiveImageRuntimes(os.Getenv)
			if err != nil {
				return errInvalidInput("%v", err)
			}
			forContext := policy.ForContextKey()
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()
			models, err := openaiapi.LoadModels(cmd.Context(), db.DB)
			if err != nil {
				return err
			}
			out := openaiapi.NewModelsReport(openaiapi.ModelsReportInput{
				For: forListener, Policy: policy, Source: openaiapi.ReportSourceShell, Models: models, ImageRuntimes: imageRuntimes,
				Auto: openaiapi.DefaultAuto(db.DB).Status(cmd.Context(), cfg.ProfileID),
			})
			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), out)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Confinement policy for a %s listener: %s (keys created with --context: %s)\n", forListener, policy, forContext)
			fmt.Fprint(w, "From this shell's flags and environment: a running server may be set up differently (`monoagentcli api status` shows what a running daemon applies).\n\n")
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "MODEL\tCONFINEMENT\tVALIDATED\tSERVED\tCONTEXT KEY\tAUTO\tIMAGES")
			for _, m := range out.Models {
				served, withContext, withAuto, withImages := "yes", "yes", "yes", "no"
				if !m.Allowed {
					served = "no (policy)"
				}
				if !m.ContextAllowed {
					withContext = "no"
				}
				if !m.AutoAllowed {
					withAuto = "no"
				}
				if slices.Contains(m.Capabilities, "image") {
					withImages = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\t%s\t%s\n", m.ID, m.Confinement, m.Validated, served, withContext, withAuto, withImages)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintf(w, "\nauto: %s\n", autoNote(out.Auto))
			return nil
		},
	}
	cmd.Flags().StringVar(&forListener, "for", "loopback", "Evaluate the policy of a loopback or a network listener")
	cmd.Flags().StringVar(&confinement, "confinement", "", "Confinement maximum: chat-only, sandboxed or any")
	cmd.Flags().StringVar(&contextConfinement, "context-confinement", "", "Strongest class a key created with --context may use: chat-only, sandboxed or any")
	cmd.Flags().StringVar(&autoConfinement, "auto-confinement", "", "Strongest class the auto model may pick: chat-only, sandboxed or any")
	return cmd
}

// The four functions below are the rules of internal/openaiapi (the MCP tool
// api_models_list applies them too), with this command's flag names in the errors.

// representativeAddr is a bind address of the given kind, for evaluating
// the per-listener default policy.
func representativeAddr(kind string) (string, error) {
	addr, err := openaiapi.ListenerAddr(kind)
	if err != nil {
		return "", errInvalidInput("--for %v", err)
	}
	return addr, nil
}

// effectiveContextMax is the strongest class a key created with --context may
// use: the explicit value (a flag), else MONOAGENT_API_CONTEXT_CONFINEMENT,
// else chat-only.
func effectiveContextMax(explicit string, getenv func(string) string) (openaiapi.Class, error) {
	c, err := openaiapi.EffectiveContextMax(explicit, getenv)
	if err != nil {
		return 0, errInvalidInput("--context-confinement (MONOAGENT_API_CONTEXT_CONFINEMENT): %v", err)
	}
	return c, nil
}

// effectiveAutoMax is the strongest class the auto model may pick: the explicit
// value (a flag), else MONOAGENT_API_AUTO_CONFINEMENT, else chat-only.
func effectiveAutoMax(explicit string, getenv func(string) string) (openaiapi.Class, error) {
	c, err := openaiapi.EffectiveAutoMax(explicit, getenv)
	if err != nil {
		return 0, errInvalidInput("--auto-confinement (MONOAGENT_API_AUTO_CONFINEMENT): %v", err)
	}
	return c, nil
}

// effectivePolicy is the confinement policy of a listener bound to addr: the
// explicit value (a flag), else MONOAGENT_API_CONFINEMENT, else the default
// for that kind of bind.
func effectivePolicy(addr, explicit string, getenv func(string) string) (openaiapi.Policy, error) {
	p, err := openaiapi.EffectivePolicy(addr, explicit, getenv)
	if err != nil {
		return openaiapi.Policy{}, errInvalidInput("%v", err)
	}
	return p, nil
}
