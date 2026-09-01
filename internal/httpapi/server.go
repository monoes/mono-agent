package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/monoes/mono-agent/internal/workflow"
)

// defaultAddr is the loopback-only default bind address, mirroring the
// webhook server's posture (internal/workflow/webhook_server.go): a
// same-user, same-machine trust boundary. Port 9321 is the webhook
// listener and 9323 the browser-extension bridge, so 9322 is used here.
const defaultAddr = "127.0.0.1:9322"

// fullOutputsHeader mirrors `workflow run --full-outputs`: when set to a
// truthy value, output items are truncated but not credential-key masked.
const fullOutputsHeader = "X-Full-Outputs"

// Server is the HTTP API server.
type Server struct {
	opts Options
	addr string
	mux  *http.ServeMux
	rt   *runtime
}

// NewServer builds a Server. The runtime (DB, profile, engine) is bootstrapped
// lazily on first request, exactly like the MCP server.
func NewServer(opts Options) (*Server, error) {
	if opts.Addr == "" {
		opts.Addr = os.Getenv("MONOAGENT_HTTPAPI_ADDR")
	}
	if opts.Addr == "" {
		opts.Addr = defaultAddr
	}
	if !opts.AllowMutations {
		opts.AllowMutations = os.Getenv("MONOAGENT_HTTPAPI_ALLOW_MUTATIONS") == "1"
	}

	rt, err := newRuntime(opts)
	if err != nil {
		return nil, err
	}

	s := &Server{opts: opts, addr: opts.Addr, rt: rt}
	s.mux = s.routes()
	return s, nil
}

// Addr returns the configured listen address.
func (s *Server) Addr() string { return s.addr }

// AllowsMutations reports whether mutating endpoints are being served.
func (s *Server) AllowsMutations() bool { return s.opts.AllowMutations }

// Close releases the underlying runtime (engine + database).
func (s *Server) Close() { s.rt.Close() }

// Handler returns the server's http.Handler (auth + routing applied),
// mainly for tests that want to drive it with httptest without a real
// listener.
func (s *Server) Handler() http.Handler { return s.mux }

// ListenAndServe binds s.Addr() and serves until ctx is cancelled, then
// gracefully shuts down. It is the entry point used by the `monoagentcli
// httpapi` command.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("httpapi: listen on %s: %w", s.addr, err)
	}
	httpSrv := &http.Server{
		Handler:      s.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // workflow run waits can be long
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

// routes builds the mux: read endpoints are always registered; mutating
// endpoints are only registered when AllowMutations is set — an
// unregistered path returns the mux's normal 404, not a 403, so no probe
// can distinguish "opted out" from "doesn't exist".
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth) // unauthenticated

	mux.Handle("GET /workflows", s.auth(s.handleWorkflowList))
	mux.Handle("GET /workflows/{id}", s.auth(s.handleWorkflowGet))
	mux.Handle("GET /workflows/{id}/executions", s.auth(s.handleWorkflowExecutions))
	mux.Handle("POST /workflows/{id}/validate", s.auth(s.handleWorkflowValidate))
	mux.Handle("GET /nodes", s.auth(s.handleNodeList))
	mux.Handle("GET /nodes/{type}/schema", s.auth(s.handleNodeSchema))
	mux.Handle("GET /hil", s.auth(s.handleHilList))

	if s.opts.AllowMutations {
		mux.Handle("POST /workflows/{id}/run", s.auth(s.handleWorkflowRun))
		mux.Handle("POST /workflows/{id}/activate", s.auth(s.handleWorkflowActivate))
		mux.Handle("POST /workflows/{id}/deactivate", s.auth(s.handleWorkflowDeactivate))
		mux.Handle("POST /hil/{id}/approve", s.auth(s.handleHilApprove))
		mux.Handle("POST /hil/{id}/reject", s.auth(s.handleHilReject))
	}

	return mux
}

// authHeaderPrefix precedes the credential value in a standard HTTP
// Authorization header for this auth scheme.
const authHeaderPrefix = "Bearer "

// auth wraps h with bearer-credential enforcement. The credential is
// generated (once, on first server start) and stored in the vault — see
// token.go.
func (s *Server) auth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want, err := ensureToken(r.Context(), s.rt.db.DB, s.rt.profileID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resolve bearer credential: "+err.Error())
			return
		}
		got := bearerCredential(r)
		if got == "" || !constantTimeEqual(got, want) {
			w.Header().Set("WWW-Authenticate", "Bearer realm=monoagentcli-httpapi")
			writeError(w, http.StatusUnauthorized, "missing or invalid bearer credential")
			return
		}
		h(w, r)
	})
}

func bearerCredential(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) <= len(authHeaderPrefix) || h[:len(authHeaderPrefix)] != authHeaderPrefix {
		return ""
	}
	return h[len(authHeaderPrefix):]
}

// wantFullOutputs mirrors `workflow run --full-outputs`: passing
// X-Full-Outputs: 1 skips credential-key redaction of output items (the
// 4KB per-item truncation still applies). It carries the same trust
// implication as the CLI flag — the caller already holds the bearer
// credential, i.e. is already authorized for this profile's data — so no
// additional gate is applied here beyond that authentication.
func wantFullOutputs(r *http.Request) bool {
	return r.Header.Get(fullOutputsHeader) == "1"
}

func redactItemsUnlessFull(r *http.Request, items []workflow.Item) []workflow.Item {
	if wantFullOutputs(r) {
		return workflow.TruncateItems(items)
	}
	return workflow.RedactAndTruncateItems(items)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":          "ok",
		"version":         s.opts.Version,
		"allow_mutations": s.opts.AllowMutations,
	})
}

// ── response helpers ─────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// statusForError maps a store/engine error to an HTTP status, mirroring
// the CLI's exit-code table (see AGENTS.md and cmd/monoagentcli/exitcodes.go):
// not-found -> 404, validation/invalid input -> 400, anything else -> 500.
// Handlers use this instead of always returning 500 so callers can branch
// on status the same way CLI callers branch on exit code.
func statusForError(err error) int {
	switch {
	case errors.Is(err, workflow.ErrWorkflowNotFound), errors.Is(err, workflow.ErrExecutionNotFound):
		return http.StatusNotFound
	case errors.Is(err, workflow.ErrNoTriggerNode),
		errors.Is(err, workflow.ErrCycleDetected),
		errors.Is(err, workflow.ErrNodeTypeUnknown),
		errors.Is(err, workflow.ErrInvalidConfig),
		errors.Is(err, workflow.ErrWorkflowInactive):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeErrorFor(w http.ResponseWriter, err error) {
	writeError(w, statusForError(err), err.Error())
}
