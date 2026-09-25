//go:build social

package linkedin

import (
	"context"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/browser"
)

// TestGetMethodByNameAcceptsPageInterface is a regression test: the executor
// (internal/action/steps.go) prepends ae.page — declared type
// browser.PageInterface, concretely *browser.RodPage or *browser.ExtensionPage
// — as args[0] to every call_bot_method dispatch. GetMethodByName's returned
// closures previously asserted args[0].(*rod.Page), a concrete type that is
// never the runtime value, so every call_bot_method step against this bot
// failed with "first arg must be *rod.Page" instead of reaching the actual
// action method.
func TestGetMethodByNameAcceptsPageInterface(t *testing.T) {
	b := &LinkedInBot{}
	fn, ok := b.GetMethodByName("list_user_posts")
	if !ok {
		t.Fatal("expected list_user_posts method to be found")
	}

	// ListUserPosts validates profileURL before ever touching the page, so
	// an empty profileURL exercises the args[0] type assertion and returns
	// cleanly without needing a live browser page.
	var page browser.PageInterface = browser.NewRodPage(nil)
	_, err := fn(context.Background(), page, "", 0, "")
	if err == nil {
		t.Fatal("expected an error for empty profileURL")
	}
	if strings.Contains(err.Error(), "must be *rod.Page") {
		t.Fatalf("closure rejected a browser.PageInterface value: %v", err)
	}
	if !strings.Contains(err.Error(), "profileURL") {
		t.Fatalf("expected the profileURL validation error, got: %v", err)
	}
}

// TestSendVerified covers the honest post-send gate: a cleared composer
// confirms the send, a rendered bubble confirms the send, and anything else
// is a verification failure that must surface as an error.
func TestSendVerified(t *testing.T) {
	cases := []struct {
		name     string
		composer string
		bubbles  []string
		message  string
		want     bool
	}{
		{"composer cleared", "", nil, "hello", true},
		{"composer whitespace only", "   \n", nil, "hello", true},
		{"bubble contains message", "hello", []string{"earlier msg", "hello"}, "hello", true},
		{"composer uncleared, no bubbles", "hello", nil, "hello", false},
		{"composer uncleared, bubble mismatch", "hello", []string{"different"}, "hello", false},
	}
	for _, c := range cases {
		if got := sendVerified(c.composer, c.bubbles, c.message); got != c.want {
			t.Errorf("%s: sendVerified(%q, %v, %q) = %v, want %v", c.name, c.composer, c.bubbles, c.message, got, c.want)
		}
	}
}

func TestArgCoercion(t *testing.T) {
	for in, want := range map[string]bool{"": true, "true": true, "false": false, "0": false, "1": true, "no": false} {
		got, err := boolArg(in, true)
		if err != nil || got != want {
			t.Errorf("boolArg(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := boolArg("maybe", true); err == nil {
		t.Error("boolArg(maybe) should fail")
	}
	if n, err := intArg("25", 5); err != nil || n != 25 {
		t.Errorf("intArg(25) = %d, %v", n, err)
	}
	if n, err := intArg("", 5); err != nil || n != 5 {
		t.Errorf("intArg('') = %d, %v", n, err)
	}
}

func TestActivityFeedURL(t *testing.T) {
	cases := map[[2]string]string{
		{"https://www.linkedin.com/in/jane-doe-test", ""}:                "https://www.linkedin.com/in/jane-doe-test/recent-activity/all/",
		{"https://www.linkedin.com/in/jane-doe-test/details/", "shares"}: "https://www.linkedin.com/in/jane-doe-test/recent-activity/shares/",
		{"https://www.linkedin.com/company/acme-test/about/", "all"}:     "https://www.linkedin.com/company/acme-test/posts/?feedView=all",
	}
	for in, want := range cases {
		got, err := activityFeedURL(in[0], in[1])
		if err != nil || got != want {
			t.Errorf("activityFeedURL(%v) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := activityFeedURL("https://www.linkedin.com/feed/", ""); err == nil {
		t.Error("feed URL should be rejected")
	}
}
