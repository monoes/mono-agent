package openaiapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

type execFunc = func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error)

// harness is a Gateway over a migrated temp database, a fake runner and the
// captured scan. Nothing in it starts a process or touches the network.
type harness struct {
	g       *Gateway
	keys    *apikeys.Store
	db      *storage.Database
	scratch string

	mu   sync.Mutex
	logs []string
}

// logged returns every line the gateway logged, formatted.
func (h *harness) logged() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.logs...)
}

func newHarness(t *testing.T, exec execFunc, mutate ...func(*Deps, *Config)) *harness {
	t.Helper()
	db := testdb.Open(t)

	h := &harness{keys: apikeys.NewStore(db.DB), db: db, scratch: filepath.Join(t.TempDir(), "workspaces")}
	f := testFuncs(t)
	deps := Deps{
		Keys:    h.keys,
		Exec:    exec,
		Bin:     func(context.Context) (string, error) { return "/fake/monomind", nil },
		Catalog: f,
		Logf: func(format string, args ...any) {
			h.mu.Lock()
			h.logs = append(h.logs, fmt.Sprintf(format, args...))
			h.mu.Unlock()
		},
		Version: "test",
	}
	cfg := Config{ScratchRoot: h.scratch, TurnTimeout: time.Minute}
	for _, m := range mutate {
		m(&deps, &cfg)
	}
	g, err := New(deps, cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.g = g
	return h
}

// key issues a key for the profile and returns its secret.
func (h *harness) key(t *testing.T, profile, name string, withContext bool) string {
	t.Helper()
	_, secret, err := h.keys.Create(context.Background(), profile, name, withContext)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

var anyPolicy = Policy{Max: Unconfined}

// scriptedExec plays events through onEvent and builds the TurnResult the
// way monomind.Exec does.
func scriptedExec(events ...monomind.Event) execFunc {
	return func(_ context.Context, _ monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		res := &monomind.TurnResult{}
		for _, ev := range events {
			onEvent(ev)
			monomind.ApplyEventToResult(res, ev)
		}
		return res, nil
	}
}

func evStart(streams bool, nativeSandbox string) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventStart, Runtime: "claude", StreamsIncrementally: streams,
		SandboxFields: monomind.SandboxFields{NativeSandbox: nativeSandbox}}
}

func evText(s string) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventAssistant, Text: s}
}

func evUsage(in, out int64) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventUsage, InputTokens: in, OutputTokens: out, HasInputTokens: true, HasOutputTokens: true}
}

func evResult(text, stop string) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventResult, Text: text, StopReason: stop}
}

func evDone(code int) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventDone, ExitCode: code, HasExitCode: true}
}

func evError(code, msg string) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventError, Code: code, ErrMessage: msg, Fatal: true}
}

// besidesTmp lists what is in a turn's folder other than the temp folder the
// gateway makes for the turn itself.
func besidesTmp(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if e.Name() != turnTmpName {
			names = append(names, e.Name())
		}
	}
	return names
}

// okTurn is a successful turn that answers text.
func okTurn(text string) execFunc {
	return scriptedExec(evStart(false, "monomind"), evText(text), evUsage(11, 7), evResult(text, monomind.StopEndTurn), evDone(0))
}
