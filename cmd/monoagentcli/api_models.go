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
	// AutoAllowed is true when the auto model may pick it: allowed, and within
	// --auto-confinement.
	AutoAllowed bool `json:"auto_allowed"`
}

// apiAutoJSON says whether the auto model works for the profile, and what is
// missing when it does not.
type apiAutoJSON struct {
	Available bool   `json:"available"`
	Missing   string `json:"missing,omitempty"`
	// KeySource is where the Jev key is, vault or env, when it is available. A key
	// from the environment is this shell's: a running server reads its own.
	KeySource string `json:"key_source,omitempty"`
	// Confinement is the strongest class it picks within: the listener's policy
	// capped by --auto-confinement (api models only).
	Confinement string `json:"confinement,omitempty"`
	// Candidates is how many models Jev would pick among: the ones the
	// listener serves within --auto-confinement (api models only).
	Candidates int `json:"candidates,omitempty"`
	// HeldBack is how many the listener serves that auto may not pick, being
	// above --auto-confinement (api models only).
	HeldBack int `json:"held_back,omitempty"`
}

// autoNote is the line the text output gives for it.
func (a apiAutoJSON) autoNote() string {
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

type apiModelsJSON struct {
	V      int `json:"v"`
	Policy struct {
		For         string `json:"for"`
		Confinement string `json:"confinement"`
		// ContextConfinement is the strongest class a key created with
		// --context may use on this listener: the context maximum, never
		// above Confinement.
		ContextConfinement string `json:"context_confinement"`
		// AutoConfinement is the strongest class the auto model may pick on this
		// listener: the auto maximum, never above Confinement.
		AutoConfinement string `json:"auto_confinement"`
		// Source says whose settings these are: "shell", this command's own flags
		// and environment, which a running server may not share.
		Source string `json:"source"`
	} `json:"policy"`
	Models []apiModelJSON `json:"models"`
	// Auto is the auto model for the active profile: Jev picks among Models.
	Auto apiAutoJSON `json:"auto"`
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
			"The auto model is held to --auto-confinement, else MONOAGENT_API_AUTO_CONFINEMENT, else chat-only, the same way.",
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
			forContext := policy.ForContextKey()
			forAuto := policy.ForAuto()
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
			out.Policy.AutoConfinement = forAuto.String()
			out.Policy.Source = "shell"
			for _, m := range models {
				if m.Alias {
					continue
				}
				out.Models = append(out.Models, apiModelJSON{
					ID: m.ID, Runtime: m.Runtime, Model: m.Model, Label: m.Label,
					Confinement: m.Class.String(), Validated: m.Validated, Allowed: policy.Allows(m.Class),
					ContextAllowed: forContext.Allows(m.Class), AutoAllowed: forAuto.Allows(m.Class),
				})
			}
			allowed, candidates := 0, 0
			for _, m := range out.Models {
				if m.Allowed {
					allowed++
				}
				if m.AutoAllowed {
					candidates++
				}
			}
			st := openaiapi.DefaultAuto(db.DB).Status(cmd.Context(), cfg.ProfileID)
			out.Auto = apiAutoJSON{Available: st.Available, Missing: st.Missing, KeySource: st.KeySource,
				Confinement: forAuto.Max.String(), Candidates: candidates, HeldBack: allowed - candidates}
			switch {
			case st.Available && allowed == 0:
				out.Auto.Available, out.Auto.Missing = false, "at least one model the listener's policy allows"
			case st.Available && candidates == 0:
				out.Auto.Available = false
				out.Auto.Missing = fmt.Sprintf("a model within --auto-confinement (%s), which holds back all %d the listener serves", forAuto, allowed)
			}
			if !out.Auto.Available {
				out.Auto.Candidates, out.Auto.HeldBack, out.Auto.KeySource, out.Auto.Confinement = 0, 0, "", ""
			}
			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), out)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Confinement policy for a %s listener: %s (keys created with --context: %s)\n", forListener, policy, forContext)
			fmt.Fprint(w, "From this shell's flags and environment: a running server may be set up differently (`monoagentcli api status` shows what a running daemon applies).\n\n")
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "MODEL\tCONFINEMENT\tVALIDATED\tSERVED\tCONTEXT KEY\tAUTO")
			for _, m := range out.Models {
				served, withContext, withAuto := "yes", "yes", "yes"
				if !m.Allowed {
					served = "no (policy)"
				}
				if !m.ContextAllowed {
					withContext = "no"
				}
				if !m.AutoAllowed {
					withAuto = "no"
				}
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\t%s\n", m.ID, m.Confinement, m.Validated, served, withContext, withAuto)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintf(w, "\nauto: %s\n", out.Auto.autoNote())
			return nil
		},
	}
	cmd.Flags().StringVar(&forListener, "for", "loopback", "Evaluate the policy of a loopback or a network listener")
	cmd.Flags().StringVar(&confinement, "confinement", "", "Confinement maximum: chat-only, sandboxed or any")
	cmd.Flags().StringVar(&contextConfinement, "context-confinement", "", "Strongest class a key created with --context may use: chat-only, sandboxed or any")
	cmd.Flags().StringVar(&autoConfinement, "auto-confinement", "", "Strongest class the auto model may pick: chat-only, sandboxed or any")
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

// effectiveAutoMax is the strongest class the auto model may pick: the explicit
// value (a flag), else MONOAGENT_API_AUTO_CONFINEMENT, else chat-only. A prompt
// can steer which model Jev picks and its author need not hold the key, so
// raising it is a choice the operator makes on purpose.
func effectiveAutoMax(explicit string, getenv func(string) string) (openaiapi.Class, error) {
	v := explicit
	if v == "" {
		v = getenv("MONOAGENT_API_AUTO_CONFINEMENT")
	}
	if v == "" {
		return openaiapi.ChatOnly, nil
	}
	p, err := openaiapi.ParsePolicy(v)
	if err != nil {
		return 0, errInvalidInput("--auto-confinement (MONOAGENT_API_AUTO_CONFINEMENT): %v", err)
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
