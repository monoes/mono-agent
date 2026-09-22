package extension

import (
	"context"
	"errors"
	"strings"

	"github.com/monoes/mono-agent/internal/capturesummary"
)

// summary.runtimes / summary.models — "which AI can write my summaries?"
//
// The side panel's "AI for summaries" picker is the desktop chat box's
// runtime → model choice (wails-app ScanAgentRuntimes/GetAgentRuntimeModels)
// asked over the extension's request channel instead of a Wails binding.
// The answers come from a capturesummary.Catalog — the same one the
// summarizer checks a capture's own choice against — so the picker can only
// offer what the bridge will then accept.
//
//	extension → Go  {"kind":"request","id":"req-…","method":"summary.runtimes"}
//	Go → extension  {"kind":"reply","id":"req-…","ok":true,
//	                 "data":{"runtimes":[{"id":"claude","version":"2.1.3"}],
//	                         "default":"claude","enabled":true}}
//
//	extension → Go  {"kind":"request","id":"req-…","method":"summary.models",
//	                 "params":{"runtime":"claude"}}
//	Go → extension  {"kind":"reply","id":"req-…","ok":true,
//	                 "data":{"runtime":"claude","models":[{"id":"claude-haiku-4-5-20251001","label":"Haiku 4.5"}]}}
//
// An empty `models` means the runtime has no list: the picker offers a
// free-text model (or the runtime's own default). A runtime the scan did not
// find is refused with code "invalid"; a scan that cannot run (monomind
// missing) is "unavailable".

// Request methods for the summary picker.
const (
	MethodSummaryRuntimes = "summary.runtimes"
	MethodSummaryModels   = "summary.models"
)

// CodeInvalid is a request whose params name something that does not exist.
const CodeInvalid = "invalid"

// SummaryRuntimes is summary.runtimes' reply.
type SummaryRuntimes struct {
	Runtimes []capturesummary.Runtime `json:"runtimes"`
	// Default is the runtime a capture that names none is summarized by;
	// empty when summaries are turned off on this bridge.
	Default string `json:"default"`
	// Enabled is false when the bridge was started with summaries "off":
	// a choice made in the picker would not be used.
	Enabled bool `json:"enabled"`
}

// SummaryModels is summary.models' reply.
type SummaryModels struct {
	Runtime string                 `json:"runtime"`
	Models  []capturesummary.Model `json:"models"`
}

// SetSummaryCatalog installs (or, with nil, removes) the two picker
// methods. defaultRuntime is the bridge's configured summary runtime,
// possibly "off".
func (s *Server) SetSummaryCatalog(cat *capturesummary.Catalog, defaultRuntime string) {
	if cat == nil {
		s.handlerMu.Lock()
		delete(s.handlers, MethodSummaryRuntimes)
		delete(s.handlers, MethodSummaryModels)
		s.handlerMu.Unlock()
		return
	}
	s.HandleRequest(MethodSummaryRuntimes, func(ctx context.Context, _ *Request, _ ProgressFunc) (any, error) {
		return listSummaryRuntimes(ctx, cat, defaultRuntime)
	})
	s.HandleRequest(MethodSummaryModels, func(ctx context.Context, req *Request, _ ProgressFunc) (any, error) {
		return listSummaryModels(ctx, cat, req.String("runtime"))
	})
}

func listSummaryRuntimes(ctx context.Context, cat *capturesummary.Catalog, defaultRuntime string) (*SummaryRuntimes, error) {
	rts, err := cat.Runtimes(ctx)
	if err != nil {
		return nil, Unavailable("cannot list agent runtimes: %v", err)
	}
	out := &SummaryRuntimes{Runtimes: rts}
	if out.Runtimes == nil {
		out.Runtimes = []capturesummary.Runtime{}
	}
	def := strings.TrimSpace(defaultRuntime)
	out.Enabled = def != "" && !capturesummary.IsDisabled(def)
	if out.Enabled {
		out.Default = def
	}
	return out, nil
}

func listSummaryModels(ctx context.Context, cat *capturesummary.Catalog, runtime string) (*SummaryModels, error) {
	if !capturesummary.ValidRuntimeID(runtime) {
		return nil, &RequestError{Code: CodeInvalid, Err: errors.New("name an installed agent runtime")}
	}
	models, err := cat.ModelsFor(ctx, runtime)
	if errors.Is(err, capturesummary.ErrUnknownRuntime) {
		return nil, &RequestError{Code: CodeInvalid, Err: err}
	}
	if err != nil {
		return nil, Unavailable("cannot list %s's models: %v", runtime, err)
	}
	return &SummaryModels{Runtime: runtime, Models: models}, nil
}
