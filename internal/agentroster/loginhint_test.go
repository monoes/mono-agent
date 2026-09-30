package agentroster

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// An auth failure from `agent test --json` keeps monomind's login_hint on
// the result, in the store and in the roster (monoes/mono-agent#271).
func TestAgentTestLoginHintIsKept(t *testing.T) {
	const out = `{"v":1,"runtime":"claude","model":"opus","status":"auth","reply":null,"latency_first_ms":null,
"latency_ms":900,"input_tokens":null,"output_tokens":null,"cost_usd":null,"cost_estimated":false,"runtime_version":"2.1.0",
"error":{"code":"auth","message":"Not logged in · Please run /login","login_hint":"claude /login"}}`
	test := func(ctx context.Context, runtime, model string, timeout time.Duration) (*monomind.AgentTestResult, error) {
		var tr monomind.AgentTestResult
		if err := json.Unmarshal([]byte(out), &tr); err != nil {
			t.Fatal(err)
		}
		return &tr, nil
	}
	db := openDB(t)
	ctx := context.Background()
	var mu sync.Mutex
	var lines []Line
	Run(ctx, []Target{{Runtime: "claude", Model: "opus"}}, RunOptions{Test: test,
		Save: func(r Result) error { return Save(ctx, db, r) }},
		func(l Line) { mu.Lock(); lines = append(lines, l); mu.Unlock() })

	var got *Result
	for _, l := range lines {
		if l.Type == "validate.result" {
			got = l.Result
		}
	}
	if got == nil || got.Status != StatusAuth || got.LoginHint != "claude /login" {
		t.Fatalf("result = %+v, want auth with the login hint", got)
	}
	if b, _ := json.Marshal(got); !strings.Contains(string(b), `"login_hint":"claude /login"`) {
		t.Errorf("result JSON = %s", b)
	}

	stored, err := List(ctx, db)
	if err != nil || len(stored) != 1 || stored[0].LoginHint != "claude /login" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	// The scan knows no hint here, so the runtime takes the result's.
	scan := &monomind.ScanResult{Agents: []monomind.ScanEntry{{ID: "claude", Installed: true}}}
	rr := Build(stored, scan, time.Now(), 0)
	if len(rr) != 1 || rr[0].LoginHint != "claude /login" || rr[0].Models[0].LoginHint != "claude /login" {
		t.Errorf("roster = %+v", rr)
	}

	// A later passing test clears the hint.
	if err := Save(ctx, db, Result{Runtime: "claude", Model: "opus", Status: StatusOK, ValidatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if stored, _ = List(ctx, db); stored[0].LoginHint != "" {
		t.Errorf("hint kept after a passing test: %+v", stored[0])
	}
}
