package extension

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A request naming the profile the side panel is "Saving into" is answered
// from that profile's capture store (scope profile:<id>) and nothing else;
// a request naming none keeps the shared brain.

func argvHas(argv []string, pair ...string) bool {
	joined := "\x00" + strings.Join(argv, "\x00") + "\x00"
	return strings.Contains(joined, "\x00"+strings.Join(pair, "\x00")+"\x00")
}

func TestDocAskScopesToProfile(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc search"] = searchJSON
	runner.replies["doc cite"] = citeJSON
	srv := serverWithRunner(t, runner)
	var asked string
	srv.SetBrainStatusSource(func(_ context.Context, profileID string) (*BrainStatus, error) {
		asked = profileID
		return &BrainStatus{Captures: 2, Indexed: 1, Pending: 1}, nil
	})

	req := &Request{Method: MethodDocAsk, Params: map[string]any{"q": "torque", "profile": "work"}}
	got, err := srv.handleDocAsk(context.Background(), req, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	argv := runner.argv("doc search")
	for _, pair := range [][]string{{"--scope", "profile:work"}, {"--store", "project"}, {"--surfaces", "chunks"}} {
		if !argvHas(argv, pair...) {
			t.Errorf("search argv %v lacks %v", argv, pair)
		}
	}
	res := got.(*AskResult)
	if res.Profile != "work" || res.Brain == nil || res.Brain.Pending != 1 || asked != "work" {
		t.Fatalf("want the work profile's brain status on the answer, got %+v (asked %q)", res, asked)
	}
}

func TestDocAskWithoutProfileKeepsSharedBrain(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc search"] = "[]"
	srv := serverWithRunner(t, runner)
	srv.SetBrainStatusSource(func(context.Context, string) (*BrainStatus, error) {
		t.Fatal("no profile, no brain status")
		return nil, nil
	})
	res, _ := ask(t, srv, "anything")
	argv := runner.argv("doc search")
	for _, a := range argv {
		if a == "--scope" || a == "--store" {
			t.Fatalf("an unscoped ask must not narrow the store: %v", argv)
		}
	}
	if res.Brain != nil || res.Profile != "" {
		t.Fatalf("unexpected profile fields: %+v", res)
	}
}

func TestDocAskBrainStatusFailureIsSilent(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc search"] = "[]"
	srv := serverWithRunner(t, runner)
	srv.SetBrainStatusSource(func(context.Context, string) (*BrainStatus, error) {
		return nil, errors.New("database locked")
	})
	req := &Request{Method: MethodDocAsk, Params: map[string]any{"q": "x", "profile": "work"}}
	got, err := srv.handleDocAsk(context.Background(), req, func(string, string) {})
	if err != nil || got.(*AskResult).Brain != nil {
		t.Fatalf("a status failure must not fail the ask: %v %+v", err, got)
	}
}

func TestKnowledgeRefusesBadProfile(t *testing.T) {
	runner := newFakeRunner()
	srv := serverWithRunner(t, runner)
	for _, bad := range []string{"../other", "a/b", `a\b`} {
		for _, call := range []func() error{
			func() error {
				_, err := srv.handleDocAsk(context.Background(), &Request{Params: map[string]any{"q": "x", "profile": bad}}, func(string, string) {})
				return err
			},
			func() error {
				_, err := srv.handleDocRelated(context.Background(), &Request{Params: map[string]any{"url": "https://example.com/", "profile": bad}}, nil)
				return err
			},
			func() error {
				_, err := srv.handleDocLookup(context.Background(), &Request{Params: map[string]any{"url": "https://example.com/", "profile": bad}}, nil)
				return err
			},
		} {
			if err := call(); err == nil {
				t.Fatalf("profile %q accepted", bad)
			}
		}
	}
	if runner.callCount() != 0 {
		t.Fatalf("a refused profile reached monomind: %v", runner.calls)
	}
}

func TestDocRelatedAndLookupScopeToProfile(t *testing.T) {
	runner := newFakeRunner()
	runner.replies["doc related"] = "[]"
	runner.replies["doc lookup"] = `{"saved":false}`
	srv := serverWithRunner(t, runner)

	if _, err := srv.handleDocRelated(context.Background(), &Request{Params: map[string]any{"url": "https://example.com/a", "profile": "work"}}, nil); err != nil {
		t.Fatal(err)
	}
	if argv := runner.argv("doc related"); !argvHas(argv, "--scope", "profile:work") {
		t.Fatalf("related argv %v lacks the profile scope", argv)
	}
	if _, err := srv.handleDocLookup(context.Background(), &Request{Params: map[string]any{"url": "https://example.com/a", "profile": "work"}}, nil); err != nil {
		t.Fatal(err)
	}
	if argv := runner.argv("doc lookup"); !argvHas(argv, "--scope", "profile:work") {
		t.Fatalf("lookup argv %v lacks the profile scope", argv)
	}
}

// TestDocLookupFallbackStaysInProfile: an older monomind without `doc
// lookup` falls back to `doc list`, and with a profile that must be the
// profile's store alone — never the shared or global brain.
func TestDocLookupFallbackStaysInProfile(t *testing.T) {
	runner := newFakeRunner()
	runner.errs["doc lookup"] = errors.New("unknown command")
	runner.replies["doc list"] = "[]"
	srv := serverWithRunner(t, runner)

	if _, err := srv.handleDocLookup(context.Background(), &Request{Params: map[string]any{"url": "https://example.com/a", "profile": "work"}}, nil); err != nil {
		t.Fatal(err)
	}
	lists := 0
	for _, c := range runner.calls {
		if subcommand(c) != "doc list" {
			continue
		}
		lists++
		if !argvHas(c, "--scope", "profile:work") || argvHas(c, "--global") {
			t.Fatalf("fallback listed outside the profile: %v", c)
		}
	}
	if lists != 1 {
		t.Fatalf("want exactly one scoped list, got %d", lists)
	}
}
