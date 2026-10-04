package openaiapi

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Whose flags and environment a models report was evaluated from. A running
// server may not share them, which the report says.
const (
	// ReportSourceShell is `monoagentcli api models`: its own flags and environment.
	ReportSourceShell = "shell"
	// ReportSourceMCP is the MCP tool api_models_list: the arguments of the call and
	// the environment of the MCP server process.
	ReportSourceMCP = "mcp"
)

// ModelReport is one model of a report.
type ModelReport struct {
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
	// Capabilities is what GET /v1/models says the model can do: "text", and "image" for
	// a model of a runtime in MONOAGENT_API_IMAGE_RUNTIMES that can write the file.
	Capabilities []string `json:"capabilities"`
}

// AutoReport says whether the auto model works for the profile, and what is
// missing when it does not.
type AutoReport struct {
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

// ReportPolicy is the confinement policy a report was evaluated for.
type ReportPolicy struct {
	For         string `json:"for"`
	Confinement string `json:"confinement"`
	// ContextConfinement is the strongest class a key created with
	// --context may use on this listener: the context maximum, never
	// above Confinement.
	ContextConfinement string `json:"context_confinement"`
	// AutoConfinement is the strongest class the auto model may pick on this
	// listener: the auto maximum, never above Confinement.
	AutoConfinement string `json:"auto_confinement"`
	// Source says whose settings these are: ReportSourceShell or ReportSourceMCP,
	// the flags and environment of whoever evaluated them, which a running server
	// may not share.
	Source string `json:"source"`
}

// ModelsReport is the document of `monoagentcli api models --json` and of the MCP
// tool api_models_list: every model of the installed runtimes with its
// confinement class, and what a listener of one kind would do with it.
type ModelsReport struct {
	V      int           `json:"v"`
	Policy ReportPolicy  `json:"policy"`
	Models []ModelReport `json:"models"`
	// Auto is the auto model for the active profile: Jev picks among Models.
	Auto AutoReport `json:"auto"`
}

// ModelsReportInput is what a report is made of. The caller brings the live
// parts, the models of the installed runtimes (LoadModels) and the auto model's
// status, so that the rules below are a function of them alone.
type ModelsReportInput struct {
	// For is the kind of listener the policy is for, "loopback" or "network".
	For string
	// Policy is that listener's policy with ContextMax and AutoMax set.
	Policy Policy
	// Source is ReportSourceShell or ReportSourceMCP.
	Source string
	Models []ModelInfo
	Auto   AutoStatus
	// ImageRuntimes are the runtimes whose models make images, as Config.ImageRuntimes
	// has them: nil is the default list, and a list with nothing in it, which
	// EffectiveImageRuntimes makes of MONOAGENT_API_IMAGE_RUNTIMES=none, is image
	// generation switched off.
	ImageRuntimes []string
}

// NewModelsReport marks which models the policy serves, which a key created with
// --context may use and which the auto model may pick, and says whether auto
// works and among how many models.
func NewModelsReport(in ModelsReportInput) ModelsReport {
	forContext, forAuto := in.Policy.ForContextKey(), in.Policy.ForAuto()
	out := ModelsReport{V: 1, Models: []ModelReport{}}
	out.Policy = ReportPolicy{
		For: in.For, Confinement: in.Policy.String(), ContextConfinement: forContext.String(),
		AutoConfinement: forAuto.String(), Source: in.Source,
	}
	images := Config{ImageRuntimes: in.ImageRuntimes} // the gateway's own rule: nil is the default list, empty is off
	allowed, candidates := 0, 0
	for _, m := range in.Models {
		if m.Alias {
			continue
		}
		row := ModelReport{
			ID: m.ID, Runtime: m.Runtime, Model: m.Model, Label: m.Label,
			Confinement: m.Class.String(), Validated: m.Validated, Allowed: in.Policy.Allows(m.Class),
			ContextAllowed: forContext.Allows(m.Class), AutoAllowed: forAuto.Allows(m.Class),
			Capabilities: images.Capabilities(m),
		}
		if row.Allowed {
			allowed++
		}
		if row.AutoAllowed {
			candidates++
		}
		out.Models = append(out.Models, row)
	}
	out.Auto = AutoReport{Available: in.Auto.Available, Missing: in.Auto.Missing, KeySource: in.Auto.KeySource,
		Confinement: forAuto.Max.String(), Candidates: candidates, HeldBack: allowed - candidates}
	switch {
	case in.Auto.Available && allowed == 0:
		out.Auto.Available, out.Auto.Missing = false, "at least one model the listener's policy allows"
	case in.Auto.Available && candidates == 0:
		out.Auto.Available = false
		out.Auto.Missing = fmt.Sprintf("a model within --auto-confinement (%s), which holds back all %d the listener serves", forAuto, allowed)
	}
	if !out.Auto.Available {
		out.Auto.Candidates, out.Auto.HeldBack, out.Auto.KeySource, out.Auto.Confinement = 0, 0, "", ""
	}
	return out
}

// NewModelCatalog is the catalog over the installed runtimes that the gateway
// has: asked of monomind and the stored roster. A caller that asks again keeps
// it and calls ModelsBound, so that calls at once share one load and a list is
// reused for ttl, instead of every call starting every runtime again.
func NewModelCatalog(db *sql.DB, ttl time.Duration) *Catalog {
	return NewCatalog(DefaultDeps(db, "").Catalog, ttl)
}

// LoadModels lists the models of the installed runtimes as the gateway's own
// catalog does, aliases included: a one-shot call, whose load ends with ctx.
func LoadModels(ctx context.Context, db *sql.DB) ([]ModelInfo, error) {
	return NewModelCatalog(db, defaultCatalogTTL).ModelsBound(ctx)
}

// ListenerAddr is a bind address of the given kind, "loopback" or "network", for
// evaluating the per-listener default policy. The error reads "must be ...": the
// caller puts the name of its own argument in front of it.
func ListenerAddr(kind string) (string, error) {
	switch kind {
	case "loopback":
		return "127.0.0.1:0", nil
	case "network":
		return "0.0.0.0:0", nil
	}
	return "", fmt.Errorf("must be loopback or network, got %q", kind)
}

// EffectivePolicy is the confinement policy of a listener bound to addr: the
// explicit value (a flag), else MONOAGENT_API_CONFINEMENT, else the default
// for that kind of bind.
func EffectivePolicy(addr, explicit string, getenv func(string) string) (Policy, error) {
	v := explicit
	if v == "" {
		v = getenv("MONOAGENT_API_CONFINEMENT")
	}
	if v == "" {
		return DefaultPolicy(addr), nil
	}
	return ParsePolicy(v)
}

// EffectiveContextMax is the strongest class a key created with --context may
// use: the explicit value (a flag), else MONOAGENT_API_CONTEXT_CONFINEMENT,
// else chat-only. Such a request carries excerpts of the profile's knowledge,
// which includes captured web pages nobody vetted, so raising it is a choice
// the operator makes on purpose.
func EffectiveContextMax(explicit string, getenv func(string) string) (Class, error) {
	return effectiveMax(explicit, "MONOAGENT_API_CONTEXT_CONFINEMENT", getenv)
}

// EffectiveAutoMax is the strongest class the auto model may pick: the explicit
// value (a flag), else MONOAGENT_API_AUTO_CONFINEMENT, else chat-only. A prompt
// can steer which model Jev picks and its author need not hold the key, so
// raising it is a choice the operator makes on purpose.
func EffectiveAutoMax(explicit string, getenv func(string) string) (Class, error) {
	return effectiveMax(explicit, "MONOAGENT_API_AUTO_CONFINEMENT", getenv)
}

// EffectiveImageRuntimes is the runtimes whose models make images: those of
// MONOAGENT_API_IMAGE_RUNTIMES, else the default list, and a list with nothing in it
// for "none" (see ParseImageRuntimes).
func EffectiveImageRuntimes(getenv func(string) string) ([]string, error) {
	return ParseImageRuntimes(getenv("MONOAGENT_API_IMAGE_RUNTIMES"))
}

func effectiveMax(explicit, envName string, getenv func(string) string) (Class, error) {
	v := explicit
	if v == "" {
		v = getenv(envName)
	}
	if v == "" {
		return ChatOnly, nil
	}
	p, err := ParsePolicy(v)
	if err != nil {
		return 0, err
	}
	return p.Max, nil
}
