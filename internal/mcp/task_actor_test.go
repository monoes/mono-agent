package mcp

// The task tools act as an agent named after the client (spec D18): agent:<client>#<four hex
// digits>. The name comes from initialize, the model cannot choose it, and every session has a
// suffix of its own, so two sessions of one client are two claimants.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/monoes/mono-agent/internal/tasks"
	"github.com/monoes/mono-agent/internal/testdb"
)

func TestClientLabel(t *testing.T) {
	long := strings.Repeat("x", 300)
	// A name whose cut at maxClientLabel falls right after the hyphen of a gap: the hyphen goes too.
	edge := strings.Repeat("a", maxClientLabel-1)
	for in, want := range map[string]string{
		"claude-code":          "claude-code",
		"Claude Desktop":       "Claude-Desktop",
		"cursor (vscode)":      "cursor-vscode",
		"a#b:c":                "a-b-c",
		"me@host.local":        "me@host.local",
		"--x--":                "x",
		"":                     "mcp",
		"   ":                  "mcp",
		"\U000065e5\U0000672c": "mcp",
		"\U0000202eevil":       "evil",
		long:                   long[:maxClientLabel],
		edge + " bbbb":         edge,
	} {
		if got := clientLabel(in); got != want {
			t.Errorf("clientLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// withSuffixes makes the servers a test builds take these suffixes, in turn.
func withSuffixes(t *testing.T, suffixes ...string) {
	t.Helper()
	was := newActorSuffix
	n := 0
	newActorSuffix = func() string {
		s := suffixes[n%len(suffixes)]
		n++
		return s
	}
	t.Cleanup(func() { newActorSuffix = was })
}

// initializeAs sends one initialize with these params, as a host does first.
func initializeAs(t *testing.T, s *Server, params map[string]any) {
	t.Helper()
	if resps := serveLines(t, s, request(1, "initialize", params)); len(resps) != 1 || resps[0]["result"] == nil {
		t.Fatalf("initialize: %v", resps)
	}
}

func TestTheTaskActorIsNamedAfterTheClientInInitialize(t *testing.T) {
	withSuffixes(t, "beef")
	s := NewServer(Options{Version: "test"})
	initializeAs(t, s, map[string]any{"protocolVersion": "2024-11-05", "clientInfo": map[string]any{"name": "Claude Code", "version": "2.1"}})
	initializeAs(t, s, map[string]any{"clientInfo": map[string]any{"name": "someone-else"}})
	if a := s.taskActor(); a.Kind != tasks.Agent || a.Name != "agent:Claude-Code#beef" {
		t.Errorf("actor = %+v, want the agent agent:Claude-Code#beef (the first client named)", a)
	}

	// Before any initialize the client is "mcp", and the first task call fixes the name: a later
	// initialize does not rename whoever holds a claim.
	early := NewServer(Options{Version: "test"})
	if got := early.taskActor().Name; got != "agent:mcp#beef" {
		t.Errorf("before initialize: %q", got)
	}
	initializeAs(t, early, map[string]any{"clientInfo": map[string]any{"name": "claude-code"}})
	if got := early.taskActor().Name; got != "agent:mcp#beef" {
		t.Errorf("a later initialize renamed the claimant: %q", got)
	}

	// An initialize without a clientInfo, as the older tests send, names no client.
	bare := NewServer(Options{Version: "test"})
	initializeAs(t, bare, map[string]any{})
	if got := bare.taskActor().Name; got != "agent:mcp#beef" {
		t.Errorf("an initialize without a client: %q", got)
	}
}

func TestTwoSessionsOfOneClientAreTwoClaimants(t *testing.T) {
	// The real generator first: the other assertions about a suffix inject it (withSuffixes), so none
	// of them would notice a generator that gives every server one value, which makes two sessions
	// of one client one claimant. Four hex digits each, and not all alike: 32 draws are alike with
	// odds of 65536^-31.
	fourHex := regexp.MustCompile(`^[0-9a-f]{4}$`)
	drawn := map[string]bool{}
	for i := 0; i < 32; i++ {
		suffix := newActorSuffix()
		if !fourHex.MatchString(suffix) {
			t.Errorf("a server's suffix is %q, want four hex digits", suffix)
		}
		drawn[suffix] = true
	}
	if len(drawn) < 2 {
		t.Errorf("32 draws of the real suffix generator gave %d distinct value, want at least 2: two sessions of one client would share a claimant", len(drawn))
	}

	withSuffixes(t, "0001", "0002")
	a, b := NewServer(Options{Version: "test"}), NewServer(Options{Version: "test"})
	for _, s := range []*Server{a, b} {
		initializeAs(t, s, map[string]any{"clientInfo": map[string]any{"name": "claude-code"}})
	}
	if an, bn := a.taskActor().Name, b.taskActor().Name; an != "agent:claude-code#0001" || bn != "agent:claude-code#0002" {
		t.Errorf("two sessions of one client: %q and %q, want a suffix each", an, bn)
	}
}

// Whatever a client calls itself, the name is one the store takes: a claim under it succeeds.
// The store, not a copy of its rule, is the judge; that holds for the reserved labels (you, agent,
// capture, chrome, os) in any case too, since an actor name always holds "agent:" and "#".
func TestEveryClientNameMakesAClaimableActor(t *testing.T) {
	db := sideDB(t, testdb.Path(t))
	store := tasks.NewStore(db.DB)
	ctx := context.Background()
	for i, client := range []string{"Claude Desktop", "a#b:c", "", strings.Repeat("Z", 300), "\U0000202eevil", "\U000065e5\U0000672c",
		"me@host.local", "you", "agent", "capture", "chrome", "os", "YOU", "Agent"} {
		s := NewServer(Options{Version: "test"})
		params, err := json.Marshal(map[string]any{"clientInfo": map[string]any{"name": client}})
		if err != nil {
			t.Fatal(err)
		}
		s.recordClient(params)
		task, _, err := store.Add(ctx, "default", tasks.AddInput{Title: fmt.Sprintf("job %d", i), Ready: true}, tasks.Actor{Kind: tasks.Human})
		if err != nil {
			t.Fatal(err)
		}
		actor := s.taskActor()
		if _, err := store.Claim(ctx, "default", task.ID, actor, 0); err != nil {
			t.Errorf("client %q: the store refuses its actor %q: %v", client, actor.Name, err)
		}
	}
}

// Requests run on goroutines of their own (Serve), so an initialize (recordClient) and a task call
// (taskActor) can meet: clientMu is there for that, and the race detector is what notices it gone
// (CI runs the suite with -race). Every caller records the client before it asks for the actor, so
// whoever asks first fixes the client's name, and every caller sees that one name.
func TestConcurrentInitializesAndTaskCallsShareOneActor(t *testing.T) {
	withSuffixes(t, "beef")
	params, err := json.Marshal(map[string]any{"clientInfo": map[string]any{"name": "claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 25; round++ {
		s := NewServer(Options{Version: "test"})
		var (
			wg    sync.WaitGroup
			start = make(chan struct{})
			names [16]string
		)
		for i := range names {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s.recordClient(params)
				names[i] = s.taskActor().Name
			}()
		}
		close(start)
		wg.Wait()
		for i, got := range names {
			if got != "agent:claude-code#beef" {
				t.Errorf("round %d, caller %d: the actor is %q, want agent:claude-code#beef", round, i, got)
			}
		}
	}
}
