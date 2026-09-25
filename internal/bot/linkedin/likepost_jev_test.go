//go:build social

package linkedin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// ---------------------------------------------------------------------------
// Fixtures: a fake LinkedIn post tab. Eval answers LikePost's finder script
// with a scripted {state,count}; CDP answers jevpick's snapshot, freshness,
// mark and unmark evaluations (adapted from internal/jevpick/pick_test.go);
// Element resolves the finder's marker, the reaction-menu selectors (when
// the menu "has" them) and jevpick markers.
// ---------------------------------------------------------------------------

const testPostURL = "https://www.linkedin.com/feed/update/urn:li:activity:7123456789/"

type fakeEl struct {
	browser.ElementHandle // nil: unexpected calls panic
	page                  *likeFakePage
	name, label           string
}

func (e *fakeEl) Click() error          { e.page.clicks = append(e.page.clicks, e.name); return nil }
func (e *fakeEl) ScrollIntoView() error { return nil }
func (e *fakeEl) Attribute(name string) (*string, error) {
	if name == "aria-label" {
		return &e.label, nil
	}
	return nil, nil
}

type likeFakePage struct {
	browser.PageInterface        // nil: unexpected calls panic
	find                  string // finder result JSON
	menu                  bool   // reaction menu selectors resolve
	state                 jevpick.PageState
	marked                map[string]int
	unmarks, cdpCalls     int
	clicks                []string
}

func newLikeFakePage(find string) *likeFakePage {
	return &likeFakePage{
		find: find,
		state: jevpick.PageState{
			URL: testPostURL, Title: "Post | LinkedIn", Text: "Alice posted: shipping day! 12 comments",
			Marker: json.RawMessage(`["m"]`), PageKey: json.RawMessage(`["k"]`),
			Guards: map[string]json.RawMessage{
				"10": json.RawMessage(`["g10"]`), "20": json.RawMessage(`["g20"]`),
				"30": json.RawMessage(`["g30"]`), "40": json.RawMessage(`["g40"]`),
			},
			Actions: []jevpick.Action{
				{ID: "e1", Kind: "click", Label: "React Like", Role: "button", Node: 10},
				{ID: "e2", Kind: "click", Label: "Like Bob's comment", Role: "button", Node: 20},
				{ID: "e3", Kind: "click", Label: "Love", Role: "button", Node: 30},
				{ID: "e4", Kind: "click", Label: "Celebrate", Role: "button", Node: 40},
			},
		},
		marked: map[string]int{},
	}
}

// Jev option ids follow the click candidates' order.
const (
	optPostLike = "1"
	optLove     = "3"
)

var (
	fakeMarkRe   = regexp.MustCompile(`nodes\.get\((\d+)\); if \(!e\?\.isConnected\) return false; e\.setAttribute\("data-monoagent-jev","([0-9a-f]+)"\)`)
	fakeUnmarkRe = regexp.MustCompile(`querySelectorAll\('\[data-monoagent-jev="([0-9a-f]+)"\]'\)`)
	fakeGuardRe  = regexp.MustCompile(`nodes\.get\((\d+)\)`)
	markerSelRe  = regexp.MustCompile(`^\[data-monoagent-jev='([0-9a-f]{16})'\]$`)
)

func (f *likeFakePage) Navigate(string) error { return nil }
func (f *likeFakePage) WaitLoad() error       { return nil }

func (f *likeFakePage) Eval(js string, _ ...interface{}) (*browser.EvalResult, error) {
	if strings.Contains(js, "candidates") {
		return browser.NewEvalResult(f.find), nil
	}
	return browser.NewEvalResult(nil), nil
}

func (f *likeFakePage) Element(selector string, _ time.Duration) (browser.ElementHandle, error) {
	if selector == "[data-monoagent-reaction-btn='true']" {
		return &fakeEl{page: f, name: "first-match", label: "React Like"}, nil
	}
	if m := markerSelRe.FindStringSubmatch(selector); m != nil {
		if node, ok := f.marked[m[1]]; ok {
			label := ""
			for _, a := range f.state.Actions {
				if a.Node == node {
					label = a.Label
				}
			}
			return &fakeEl{page: f, name: fmt.Sprintf("jev-node-%d", node), label: label}, nil
		}
		return nil, errors.New("marker not found")
	}
	for _, sel := range reactionButtonSelectors {
		if selector == sel && f.menu {
			return &fakeEl{page: f, name: selector}, nil
		}
	}
	return nil, fmt.Errorf("element not found: %s", selector)
}

func (f *likeFakePage) CDP(method string, params map[string]interface{}) (map[string]interface{}, error) {
	f.cdpCalls++
	if method != "Runtime.evaluate" {
		return map[string]interface{}{}, nil
	}
	expr := params["expression"].(string)
	var value interface{}
	switch {
	case strings.Contains(expr, "state?.marker"):
		value = []interface{}{"m"}
	case strings.Contains(expr, "c.pageKey(),c.guard"):
		var g interface{}
		_ = json.Unmarshal(f.state.Guards[fakeGuardRe.FindStringSubmatch(expr)[1]], &g)
		value = []interface{}{[]interface{}{"k"}, g}
	case fakeMarkRe.MatchString(expr):
		m := fakeMarkRe.FindStringSubmatch(expr)
		var node int
		_ = json.Unmarshal([]byte(m[1]), &node)
		f.marked[m[2]] = node
		value = true
	case fakeUnmarkRe.MatchString(expr):
		f.unmarks++
		delete(f.marked, fakeUnmarkRe.FindStringSubmatch(expr)[1])
		value = true
	default: // the snapshot
		raw, _ := json.Marshal(f.state)
		_ = json.Unmarshal(raw, &value)
	}
	return map[string]interface{}{"result": map[string]interface{}{"value": value}}, nil
}

func noSleep(t *testing.T) {
	t.Helper()
	old := likePostSleep
	likePostSleep = func(time.Duration) {}
	t.Cleanup(func() { likePostSleep = old })
}

// newBot returns a LinkedInBot, with a Jev picker pointed at a jevtest
// server answering target=answer when answer != "".
func newBot(t *testing.T, answer string) (*LinkedInBot, *jevtest.Server) {
	t.Helper()
	noSleep(t)
	b := &LinkedInBot{}
	if answer == "" {
		return b, nil
	}
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": answer}))
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	b.SetJevPicker(c, 0)
	return b, srv
}

func intents(srv *jevtest.Server) []string {
	var out []string
	for _, r := range srv.Requests() {
		raw, _ := json.Marshal(r.Questions["target"].Instructions)
		var ins struct {
			Intent string `json:"intent"`
		}
		_ = json.Unmarshal(raw, &ins)
		out = append(out, ins.Intent)
	}
	return out
}

func wantClicks(t *testing.T, f *likeFakePage, want ...string) {
	t.Helper()
	if strings.Join(f.clicks, ",") != strings.Join(want, ",") {
		t.Fatalf("clicks = %v, want %v", f.clicks, want)
	}
}

// ---------------------------------------------------------------------------
// Main reaction button
// ---------------------------------------------------------------------------

func TestLikePostExactlyOneMatchSkipsJev(t *testing.T) {
	b, srv := newBot(t, optPostLike)
	f := newLikeFakePage(`{"state":"marked","count":1}`)
	if err := b.LikePost(context.Background(), f, testPostURL, "like"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f, "first-match")
	if srv.Calls() != 0 || f.cdpCalls != 0 {
		t.Fatalf("Jev consulted on a unique match: calls=%d cdp=%d", srv.Calls(), f.cdpCalls)
	}
}

func TestLikePostNoMatchJevPicksAndClicksMarkedElement(t *testing.T) {
	b, srv := newBot(t, optPostLike)
	f := newLikeFakePage(`{"state":"not_found","count":0}`)
	if err := b.LikePost(context.Background(), f, testPostURL, "like"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f, "jev-node-10")
	if srv.Calls() != 1 {
		t.Fatalf("jev calls = %d, want 1", srv.Calls())
	}
	if len(f.marked) != 0 || f.unmarks != 1 {
		t.Fatalf("marker not released: marked=%v unmarks=%d", f.marked, f.unmarks)
	}
	in := intents(srv)[0]
	if !strings.Contains(in, testPostURL) || !strings.Contains(in, "not a Like button that belongs to a comment") {
		t.Fatalf("intent = %q", in)
	}
}

func TestLikePostSeveralMatchesAsksJevWithThePost(t *testing.T) {
	b, srv := newBot(t, optPostLike)
	f := newLikeFakePage(`{"state":"marked","count":3}`)
	if err := b.LikePost(context.Background(), f, testPostURL, "like"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f, "jev-node-10")
	if in := intents(srv); len(in) != 1 || !strings.Contains(in[0], testPostURL) {
		t.Fatalf("intents = %q", in)
	}
}

func TestLikePostSeveralMatchesJevNoneKeepsFirstMatch(t *testing.T) {
	b, srv := newBot(t, "NONE")
	f := newLikeFakePage(`{"state":"marked","count":3}`)
	if err := b.LikePost(context.Background(), f, testPostURL, "like"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f, "first-match")
	if srv.Calls() != 1 || len(f.marked) != 0 {
		t.Fatalf("calls=%d marked=%v", srv.Calls(), f.marked)
	}
}

func TestLikePostJevPickAlreadyReacted(t *testing.T) {
	b, _ := newBot(t, optPostLike)
	f := newLikeFakePage(`{"state":"not_found","count":0}`)
	f.state.Actions[0].Label = "Remove your reaction Like"
	if err := b.LikePost(context.Background(), f, testPostURL, "like"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f)
	if len(f.marked) != 0 {
		t.Fatalf("marker not released: %v", f.marked)
	}
}

func TestLikePostNoMatchJevNoneKeepsNotFoundError(t *testing.T) {
	b, _ := newBot(t, "NONE")
	f := newLikeFakePage(`{"state":"not_found","count":0}`)
	err := b.LikePost(context.Background(), f, testPostURL, "like")
	if err == nil || !strings.Contains(err.Error(), "could not find reaction button") || !strings.Contains(err.Error(), "jev fallback") {
		t.Fatalf("err = %v", err)
	}
	wantClicks(t, f)
}

func TestLikePostWithoutPickerIsUnchanged(t *testing.T) {
	b, _ := newBot(t, "")
	f := newLikeFakePage(`{"state":"not_found","count":0}`)
	err := b.LikePost(context.Background(), f, testPostURL, "like")
	want := "linkedin: could not find reaction button on " + testPostURL + " (not_found)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if f.cdpCalls != 0 {
		t.Fatalf("CDP used without a picker: %d", f.cdpCalls)
	}

	f = newLikeFakePage(`{"state":"marked","count":3}`)
	if err := b.LikePost(context.Background(), f, testPostURL, "like"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f, "first-match")

	f = newLikeFakePage(`{"state":"already_reacted","count":2}`)
	if err := b.LikePost(context.Background(), f, testPostURL, "like"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f)
}

// A page without CDP (no jevpick.Page) behaves as without a picker.
func TestLikePostPageWithoutCDPIsUnchanged(t *testing.T) {
	b, srv := newBot(t, optPostLike)
	f := newLikeFakePage(`{"state":"marked","count":3}`)
	var page browser.PageInterface = struct {
		browser.PageInterface
	}{f}
	if _, ok := page.(jevpick.Page); ok {
		t.Fatal("test page must not speak CDP")
	}
	if err := b.LikePost(context.Background(), page, testPostURL, "like"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f, "first-match")
	if srv.Calls() != 0 {
		t.Fatalf("jev calls = %d", srv.Calls())
	}
}

// ---------------------------------------------------------------------------
// Reaction menu: never substitute the requested reaction
// ---------------------------------------------------------------------------

func TestLikePostReactionFoundBySelectorSkipsJev(t *testing.T) {
	b, srv := newBot(t, optLove)
	f := newLikeFakePage(`{"state":"marked","count":1}`)
	f.menu = true
	if err := b.LikePost(context.Background(), f, testPostURL, "love"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f, reactionButtonSelectors["love"])
	if srv.Calls() != 0 {
		t.Fatalf("jev calls = %d", srv.Calls())
	}
}

func TestLikePostMissingReactionJevPicksIt(t *testing.T) {
	b, srv := newBot(t, optLove)
	f := newLikeFakePage(`{"state":"marked","count":1}`)
	if err := b.LikePost(context.Background(), f, testPostURL, "love"); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f, "jev-node-30")
	in := intents(srv)
	if len(in) != 1 || !strings.Contains(in[0], `"Love" reaction`) {
		t.Fatalf("intents = %q", in)
	}
	if len(f.marked) != 0 {
		t.Fatalf("marker not released: %v", f.marked)
	}
}

func TestLikePostMissingReactionWithoutPickerFails(t *testing.T) {
	b, _ := newBot(t, "")
	f := newLikeFakePage(`{"state":"marked","count":1}`)
	err := b.LikePost(context.Background(), f, testPostURL, "love")
	if err == nil || !strings.Contains(err.Error(), "love reaction not found") {
		t.Fatalf("err = %v, want reaction not found", err)
	}
	// Previously this clicked the plain Like button instead.
	wantClicks(t, f)
}

func TestLikePostMissingReactionJevNoneFails(t *testing.T) {
	b, srv := newBot(t, "NONE")
	f := newLikeFakePage(`{"state":"marked","count":1}`)
	err := b.LikePost(context.Background(), f, testPostURL, "celebrate")
	if err == nil || !strings.Contains(err.Error(), "celebrate reaction not found") || !strings.Contains(err.Error(), "jev fallback") {
		t.Fatalf("err = %v", err)
	}
	wantClicks(t, f)
	if srv.Calls() != 1 {
		t.Fatalf("jev calls = %d", srv.Calls())
	}
}

func TestLikePostUnknownReactionFails(t *testing.T) {
	b, _ := newBot(t, "")
	f := newLikeFakePage(`{"state":"marked","count":1}`)
	err := b.LikePost(context.Background(), f, testPostURL, "wow")
	if err == nil || !strings.Contains(err.Error(), `unknown reaction "wow"`) {
		t.Fatalf("err = %v", err)
	}
	wantClicks(t, f)
	// Case and spacing are normalised.
	f.menu = true
	if err := b.LikePost(context.Background(), f, testPostURL, " Love "); err != nil {
		t.Fatal(err)
	}
	wantClicks(t, f, reactionButtonSelectors["love"])
}

// The node layer enables the picker through this optional interface.
func TestLinkedInBotExposesJevSetter(t *testing.T) {
	var a interface{} = &LinkedInBot{}
	if _, ok := a.(interface{ SetJevPicker(*jev.Client, float64) }); !ok {
		t.Fatal("LinkedInBot must expose SetJevPicker")
	}
}
