//go:build social

package linkedin

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

const (
	testPost    = "https://www.linkedin.com/feed/update/urn:li:activity:7100000000000000001/"
	commentA    = "urn:li:comment:(ugcPost:7100000000000000009,7100000000000000101)"
	replyA      = "urn:li:comment:(ugcPost:7100000000000000009,7100000000000000102)"
	commentB    = "urn:li:comment:(ugcPost:7100000000000000009,7100000000000000103)"
	lateComment = "urn:li:comment:(ugcPost:7100000000000000009,7100000000000000104)"
	lateReply   = "urn:li:comment:(ugcPost:7100000000000000009,7100000000000000105)"
)

var postRoute = bottest.Route{Pattern: "https://www.linkedin.com/feed/update/*", File: "testdata/post.html"}

func postButtonLabel(t *testing.T, p *bottest.Page) string {
	return evalString(t, p, `document.getElementById('post-like').getAttribute('aria-label') + '|' + document.getElementById('post-like').getAttribute('aria-pressed')`)
}

func TestFixtureCSPBlocksPlainEval(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	p, _ := newPage(t, b, postRoute)
	if err := p.Navigate(testPost); err != nil {
		t.Fatal(err)
	}
	if res, _ := p.Eval(`() => 1`); res != nil && res.Raw() != nil {
		t.Fatalf("plain Eval ran under the LinkedIn-like CSP (got %v); the fixture no longer reproduces production", res.Raw())
	}
}

func TestLikePost(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)

	t.Run("like", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		res, err := call(t, &LinkedInBot{}, p, "like_post", testPost, "")
		if err != nil {
			t.Fatal(err)
		}
		m := res.(map[string]interface{})
		if m["already_reacted"] != false || m["reaction"] != "like" {
			t.Fatalf("result = %v", m)
		}
		if got := postButtonLabel(t, p); got != "Unreact Like|true" {
			t.Fatalf("post button = %q", got)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], `"target":"post","type":"LIKE"`) {
			t.Fatalf("writes = %v, want one post LIKE", w)
		}
	})

	t.Run("already reacted is left alone", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		res, err := call(t, &LinkedInBot{}, p, "like_post", testPost+"?state=reacted", "like")
		if err != nil {
			t.Fatal(err)
		}
		m := res.(map[string]interface{})
		if m["already_reacted"] != true || m["current_reaction"] != "celebrate" {
			t.Fatalf("result = %v", m)
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("a reacted post was touched: %v", w)
		}
		if got := postButtonLabel(t, p); got != "Unreact Celebrate|true" {
			t.Fatalf("post button = %q", got)
		}
	})

	t.Run("celebrate via the reactions menu trigger", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := call(t, &LinkedInBot{}, p, "like_post", testPost, "celebrate"); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], `"target":"post","type":"CELEBRATE"`) {
			t.Fatalf("writes = %v, want one CELEBRATE", w)
		}
	})

	t.Run("insightful via a real hover when there is no trigger", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := call(t, &LinkedInBot{}, p, "like_post", testPost+"?nomenu=1", "insightful"); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], `"target":"post","type":"INSIGHTFUL"`) {
			t.Fatalf("writes = %v, want one INSIGHTFUL", w)
		}
	})

	t.Run("unconfirmed reaction is an error", func(t *testing.T) {
		p, _ := newPage(t, b, postRoute)
		_, err := call(t, &LinkedInBot{}, p, "like_post", testPost+"?broken=react", "like")
		if err == nil || !strings.Contains(err.Error(), "not confirmed") {
			t.Fatalf("err = %v, want not confirmed", err)
		}
	})

	t.Run("unknown reaction", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := call(t, &LinkedInBot{}, p, "like_post", testPost, "wow"); err == nil {
			t.Fatal("want error")
		}
		if len(rec.Requests()) != 0 {
			t.Fatal("navigated for an invalid reaction")
		}
	})

	t.Run("unrecognisable button without jev fails", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := call(t, &LinkedInBot{}, p, "like_post", testPost+"?obscure=1", "like"); err == nil {
			t.Fatal("want error")
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})
}

func TestLikePostJev(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)

	t.Run("jev picks the post button", func(t *testing.T) {
		bot, srv := jevBot(t, "Applaud this")
		p, rec := newPage(t, b, postRoute)
		if _, err := call(t, bot, p, "like_post", testPost+"?obscure=1", "like"); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], `"target":"post"`) {
			t.Fatalf("writes = %v", w)
		}
		if in := jevIntents(srv); len(in) != 1 || !strings.Contains(in[0], "not a Like button that belongs to a comment") {
			t.Fatalf("intents = %v", in)
		}
	})

	t.Run("jev picking a comment's like is refused", func(t *testing.T) {
		bot, _ := jevBot(t, "React Like to Avery")
		p, rec := newPage(t, b, postRoute)
		_, err := call(t, bot, p, "like_post", testPost+"?obscure=1", "like")
		if err == nil || !strings.Contains(err.Error(), "not the post's reaction button") {
			t.Fatalf("err = %v", err)
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("jev NONE fails", func(t *testing.T) {
		bot, _ := jevBot(t, "")
		p, rec := newPage(t, b, postRoute)
		if _, err := call(t, bot, p, "like_post", testPost+"?obscure=1", "like"); err == nil {
			t.Fatal("want error")
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("unique selector match never asks jev", func(t *testing.T) {
		bot, srv := jevBot(t, "React Like")
		p, _ := newPage(t, b, postRoute)
		if _, err := call(t, bot, p, "like_post", testPost, "like"); err != nil {
			t.Fatal(err)
		}
		if srv.Calls() != 0 {
			t.Fatalf("jev consulted %d times", srv.Calls())
		}
	})
}

func TestListPostComments(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	ids := func(res interface{}) []string {
		var out []string
		for _, c := range res.([]map[string]interface{}) {
			out = append(out, c["id"].(string))
		}
		return out
	}

	t.Run("with replies (default)", func(t *testing.T) {
		p, _ := newPage(t, b, postRoute)
		res, err := call(t, &LinkedInBot{}, p, "list_post_comments", testPost, 50, "")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{commentA, replyA, commentB, lateReply, lateComment}
		if got := ids(res); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("ids = %v, want %v", got, want)
		}
		c := res.([]map[string]interface{})
		if c[0]["author"] != "Avery Quill" || c[0]["text"] != "Love the new teeth profile." || c[0]["likes_count"] != 3 ||
			c[0]["author_url"] != "https://www.linkedin.com/in/avery-quill-test/" || c[0]["parent_id"] != nil || c[0]["reply_count"] != 1 {
			t.Fatalf("first comment = %v", c[0])
		}
		if c[1]["parent_id"] != commentA || c[1]["is_reply"] != true {
			t.Fatalf("reply = %v", c[1])
		}
		if c[2]["liked"] != true {
			t.Fatalf("comment B should read as liked: %v", c[2])
		}
	})

	t.Run(`includeReplies "false" means false`, func(t *testing.T) {
		p, _ := newPage(t, b, postRoute)
		res, err := call(t, &LinkedInBot{}, p, "list_post_comments", testPost, "50", "false")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{commentA, commentB, lateComment}
		if got := ids(res); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	})

	t.Run("max", func(t *testing.T) {
		p, _ := newPage(t, b, postRoute)
		res, err := call(t, &LinkedInBot{}, p, "list_post_comments", testPost, float64(2), true)
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(res); len(got) != 2 {
			t.Fatalf("ids = %v", got)
		}
	})
}

func TestLikeComment(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)

	t.Run("like", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		res, err := call(t, &LinkedInBot{}, p, "like_comment", testPost, commentA)
		if err != nil {
			t.Fatal(err)
		}
		if res.(map[string]interface{})["already_liked"] != false {
			t.Fatalf("res = %v", res)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], "7100000000000000101") {
			t.Fatalf("writes = %v, want one like on comment 101", w)
		}
	})

	t.Run("already liked is left alone", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		res, err := call(t, &LinkedInBot{}, p, "like_comment", testPost, commentB)
		if err != nil {
			t.Fatal(err)
		}
		if res.(map[string]interface{})["already_liked"] != true {
			t.Fatalf("res = %v", res)
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("collapsed reply is expanded first", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := call(t, &LinkedInBot{}, p, "like_comment", testPost, lateReply); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], "7100000000000000105") {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("missing comment", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		_, err := call(t, &LinkedInBot{}, p, "like_comment", testPost, "urn:li:comment:(ugcPost:1,2)")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("err = %v", err)
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("unconfirmed like is an error", func(t *testing.T) {
		p, _ := newPage(t, b, postRoute)
		if _, err := call(t, &LinkedInBot{}, p, "like_comment", testPost+"?broken=react", commentA); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestCommentOnPost(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)

	t.Run("top-level comment", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		res, err := call(t, &LinkedInBot{}, p, "comment_on_post", testPost, "Congrats on the launch — well done!", "")
		if err != nil {
			t.Fatal(err)
		}
		m := res.(map[string]interface{})
		if id, _ := m["comment_id"].(string); !strings.HasPrefix(id, "urn:li:comment:") {
			t.Fatalf("res = %v", m)
		}
		w := writes(rec)
		if len(w) != 1 || !strings.Contains(w[0], `"parent":null`) || !strings.Contains(w[0], "Congrats on the launch") {
			t.Fatalf("writes = %v", w)
		}
		if got := evalString(t, p, `document.querySelector('#comment-list > article .update-components-text').innerText`); got != "Congrats on the launch — well done!" {
			t.Fatalf("first comment = %q", got)
		}
	})

	t.Run("reply to a comment", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := call(t, &LinkedInBot{}, p, "comment_on_post", testPost, "Thanks Avery", commentA); err != nil {
			t.Fatal(err)
		}
		w := writes(rec)
		if len(w) != 1 || !strings.Contains(w[0], "7100000000000000101") {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("submit that does nothing is an error", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		_, err := call(t, &LinkedInBot{}, p, "comment_on_post", testPost+"?broken=submit", "Hello there", "")
		if err == nil || !strings.Contains(err.Error(), "not confirmed") {
			t.Fatalf("err = %v", err)
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("no reply button fails without typing anywhere", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		_, err := call(t, &LinkedInBot{}, p, "comment_on_post", testPost+"?noreplybtn=1", "Hi", commentA)
		if err == nil {
			t.Fatal("want error")
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
		if got := evalString(t, p, `document.querySelector('#main-box .ql-editor').innerText.trim()`); got != "" {
			t.Fatalf("typed into the post's own box: %q", got)
		}
	})
}
