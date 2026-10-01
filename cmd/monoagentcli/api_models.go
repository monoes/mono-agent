package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/openaiapi"
)

type apiModelJSON struct {
	ID          string `json:"id"`
	Runtime     string `json:"runtime"`
	Model       string `json:"model"`
	Label       string `json:"label"`
	Confinement string `json:"confinement"`
	Validated   bool   `json:"validated"`
	Allowed     bool   `json:"allowed"`
	// ContextAllowed is true when a key created with --context may use the model.
	ContextAllowed bool `json:"context_allowed"`
}

type apiModelsJSON struct {
	V      int `json:"v"`
	Policy struct {
		For         string `json:"for"`
		Confinement string `json:"confinement"`
		// ContextConfinement is the strongest class a key created with
		// --context may use on this listener: the context maximum, never
		// above Confinement.
		ContextConfinement string `json:"context_confinement"`
		// Source says whose settings these are: "shell", this command's own flags
		// and environment, which a running server may not share.
		Source string `json:"source"`
	} `json:"policy"`
	Models []apiModelJSON `json:"models"`
}

func newAPIModelsCmd(cfg *globalConfig) *cobra.Command {
	var forListener, confinement, contextConfinement string
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List the models /v1/models would serve, with each one's confinement class",
		Long: "Lists every model of the installed agent runtimes with its confinement class (chat-only, sandboxed or " +
			"unconfined) and whether the confinement policy of a loopback or a network listener allows it. " +
			"The policy is --confinement, else MONOAGENT_API_CONFINEMENT, else the listener's default: any on " +
			"loopback, chat-only on a network bind. A key created with --context is held to --context-confinement, " +
			"else MONOAGENT_API_CONTEXT_CONFINEMENT, else chat-only, and never above the listener's policy.",
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
			forContext := policy.ForContextKey()
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()
			catalog := openaiapi.NewCatalog(openaiapi.DefaultDeps(db.DB, getVersion()).Catalog, time.Minute)
			models, err := catalog.Models(cmd.Context())
			if err != nil {
				return err
			}

			out := apiModelsJSON{V: 1, Models: []apiModelJSON{}}
			out.Policy.For, out.Policy.Confinement, out.Policy.ContextConfinement = forListener, policy.String(), forContext.String()
			out.Policy.Source = "shell"
			for _, m := range models {
				if m.Alias {
					continue
				}
				out.Models = append(out.Models, apiModelJSON{
					ID: m.ID, Runtime: m.Runtime, Model: m.Model, Label: m.Label,
					Confinement: m.Class.String(), Validated: m.Validated, Allowed: policy.Allows(m.Class),
					ContextAllowed: forContext.Allows(m.Class),
				})
			}
			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), out)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Confinement policy for a %s listener: %s (keys created with --context: %s)\n", forListener, policy, forContext)
			fmt.Fprint(w, "From this shell's flags and environment: a running server may be set up differently (`monoagentcli api status` shows what it applies).\n\n")
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "MODEL\tCONFINEMENT\tVALIDATED\tSERVED\tCONTEXT KEY")
			for _, m := range out.Models {
				served, withContext := "yes", "yes"
				if !m.Allowed {
					served = "no (policy)"
				}
				if !m.ContextAllowed {
					withContext = "no"
				}
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\n", m.ID, m.Confinement, m.Validated, served, withContext)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&forListener, "for", "loopback", "Evaluate the policy of a loopback or a network listener")
	cmd.Flags().StringVar(&confinement, "confinement", "", "Confinement maximum: chat-only, sandboxed or any")
	cmd.Flags().StringVar(&contextConfinement, "context-confinement", "", "Strongest class a key created with --context may use: chat-only, sandboxed or any")
	return cmd
}

// representativeAddr is a bind address of the given kind, for evaluating
// the per-listener default policy.
func representativeAddr(kind string) (string, error) {
	switch kind {
	case "loopback":
		return "127.0.0.1:0", nil
	case "network":
		return "0.0.0.0:0", nil
	}
	return "", errInvalidInput("--for must be loopback or network, got %q", kind)
}

// effectiveContextMax is the strongest class a key created with --context may
// use: the explicit value (a flag), else MONOAGENT_API_CONTEXT_CONFINEMENT,
// else chat-only. Such a request carries excerpts of the profile's knowledge,
// which includes captured web pages nobody vetted, so raising it is a choice
// the operator makes on purpose.
func effectiveContextMax(explicit string, getenv func(string) string) (openaiapi.Class, error) {
	v := explicit
	if v == "" {
		v = getenv("MONOAGENT_API_CONTEXT_CONFINEMENT")
	}
	if v == "" {
		return openaiapi.ChatOnly, nil
	}
	p, err := openaiapi.ParsePolicy(v)
	if err != nil {
		return 0, errInvalidInput("--context-confinement (MONOAGENT_API_CONTEXT_CONFINEMENT): %v", err)
	}
	return p.Max, nil
}

// effectivePolicy is the confinement policy of a listener bound to addr: the
// explicit value (a flag), else MONOAGENT_API_CONFINEMENT, else the default
// for that kind of bind.
func effectivePolicy(addr, explicit string, getenv func(string) string) (openaiapi.Policy, error) {
	v := explicit
	if v == "" {
		v = getenv("MONOAGENT_API_CONFINEMENT")
	}
	if v == "" {
		return openaiapi.DefaultPolicy(addr), nil
	}
	p, err := openaiapi.ParsePolicy(v)
	if err != nil {
		return openaiapi.Policy{}, errInvalidInput("%v", err)
	}
	return p, nil
}
