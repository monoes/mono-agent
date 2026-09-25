//go:build !nosocial

package linkedin

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

func postIDs(t *testing.T, res interface{}) []string {
	t.Helper()
	var out []string
	for _, p := range res.([]map[string]interface{}) {
		out = append(out, p["activity_id"].(string))
	}
	return out
}

func TestListUserPosts(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)

	t.Run("company posts, classic markup, show more", func(t *testing.T) {
		p, rec := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/company/*", File: "testdata/activity.html"})
		res, err := call(t, &LinkedInBot{}, p, "list_user_posts", "https://www.linkedin.com/company/example-labs-test/", "10", "")
		if err != nil {
			t.Fatal(err)
		}
		want := "7300000000000000001,7300000000000000002,7300000000000000003,7300000000000000004,7300000000000000005"
		if got := strings.Join(postIDs(t, res), ","); got != want {
			t.Fatalf("ids = %s, want %s", got, want)
		}
		first := res.([]map[string]interface{})[0]
		if first["url"] != "https://www.linkedin.com/feed/update/urn:li:activity:7300000000000000001/" || first["author"] != "Example Labs" ||
			first["likes_count"] != 120 || first["comments_count"] != 14 || first["reposts_count"] != 6 || first["timestamp"] != "2d" ||
			first["text_preview"] != "Our Q3 roadmap is live." {
			t.Fatalf("first = %v", first)
		}
		if third := res.([]map[string]interface{})[2]; third["url"] != "https://www.linkedin.com/feed/update/urn:li:ugcPost:7300000000000000003/" {
			t.Fatalf("ugcPost url = %v", third["url"])
		}
		if len(rec.Matching("GET", "https://www.linkedin.com/company/example-labs-test/posts/?feedView=all")) != 1 {
			t.Fatalf("requests = %v", rec.Requests())
		}
	})

	t.Run("member shares, max", func(t *testing.T) {
		p, rec := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/in/*", File: "testdata/activity.html"})
		res, err := call(t, &LinkedInBot{}, p, "list_user_posts", "https://www.linkedin.com/in/finn-example-test", float64(2), "shares")
		if err != nil {
			t.Fatal(err)
		}
		if got := postIDs(t, res); len(got) != 2 {
			t.Fatalf("ids = %v", got)
		}
		if len(rec.Matching("GET", "https://www.linkedin.com/in/finn-example-test/recent-activity/shares/")) != 1 {
			t.Fatalf("requests = %v", rec.Requests())
		}
	})

	t.Run("server-driven markup: permalinks only", func(t *testing.T) {
		p, _ := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/in/*", File: "testdata/feed_sdui.html"})
		res, err := call(t, &LinkedInBot{}, p, "list_user_posts", "https://www.linkedin.com/in/dana-fictive-test/", 10, "all")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(postIDs(t, res), ","); got != "7200000000000000001,7200000000000000002" {
			t.Fatalf("ids = %s", got)
		}
		first := res.([]map[string]interface{})[0]
		if first["author"] != "Dana Fictive" || first["text_preview"] != "Five lessons from shipping our first offline-first app." {
			t.Fatalf("first = %v", first)
		}
	})

	t.Run("own activity: social-proof like counts, reposts keep their own author", func(t *testing.T) {
		p, _ := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/in/*", File: "testdata/activity_own.html"})
		res, err := call(t, &LinkedInBot{}, p, "list_user_posts", "https://www.linkedin.com/in/owen-owner-test/", 10, "all")
		if err != nil {
			t.Fatal(err)
		}
		posts := res.([]map[string]interface{})
		if got := strings.Join(postIDs(t, res), ","); got != "7400000000000000001,7400000000000000002,7400000000000000003,7400000000000000004" {
			t.Fatalf("ids = %s", got)
		}
		want := []struct {
			author, authorURL string
			likes, comments   int
		}{
			{"Rhea Partner", "https://www.linkedin.com/in/rhea-partner-test", 5, 0},       // collaboration: header links the owner
			{"Acme Widgets", "https://www.linkedin.com/company/acme-widgets-test", 15, 8}, // repost of a company post
			{"Owen Owner", "https://www.linkedin.com/in/owen-owner-test", 7, 0},           // "You and 6 others", no fallback number
			{"Owen Owner", "https://www.linkedin.com/in/owen-owner-test", 29, 2},          // plain reaction count
		}
		for i, w := range want {
			got := posts[i]
			if got["author"] != w.author || got["author_url"] != w.authorURL || got["likes_count"] != w.likes || got["comments_count"] != w.comments {
				t.Errorf("post %d = author %q url %q likes %v comments %v; want %q %q %d %d",
					i, got["author"], got["author_url"], got["likes_count"], got["comments_count"], w.author, w.authorURL, w.likes, w.comments)
			}
		}
	})

	t.Run("not a profile URL", func(t *testing.T) {
		p, _ := newPage(t, b)
		if _, err := call(t, &LinkedInBot{}, p, "list_user_posts", "https://www.linkedin.com/feed/", 5, ""); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestSearchPosts(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	p, rec := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/search/results/content/*", File: "testdata/activity.html"})
	res, err := call(t, &LinkedInBot{}, p, "search_posts", "offline first", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := postIDs(t, res); len(got) != 3 {
		t.Fatalf("ids = %v", got)
	}
	if len(rec.Matching("GET", "https://www.linkedin.com/search/results/content/?keywords=offline+first*")) != 1 {
		t.Fatalf("requests = %v", rec.Requests())
	}
}

func TestLikePostServerDrivenMarkup(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	route := bottest.Route{Pattern: "https://www.linkedin.com/feed/update/*", File: "testdata/feed_sdui.html"}
	const post1 = "https://www.linkedin.com/feed/update/urn:li:activity:7200000000000000001/"
	const post2 = "https://www.linkedin.com/feed/update/urn:li:activity:7200000000000000002/"

	t.Run("like, found through the permalink among several posts", func(t *testing.T) {
		p, rec := newPage(t, b, route)
		if _, err := call(t, &LinkedInBot{}, p, "like_post", post1, "like"); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], `"target":"post-1","type":"LIKE"`) {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("support through the toolbar", func(t *testing.T) {
		p, rec := newPage(t, b, route)
		if _, err := call(t, &LinkedInBot{}, p, "like_post", post1+"?single=1", "support"); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], `"type":"SUPPORT"`) {
			t.Fatalf("writes = %v", w)
		}
		if got := evalString(t, p, `document.getElementById('like-1').getAttribute('aria-label')`); got != "Reaction button state: Support" {
			t.Fatalf("label = %q", got)
		}
	})

	t.Run("reaction state other than no reaction is already reacted", func(t *testing.T) {
		p, rec := newPage(t, b, route)
		res, err := call(t, &LinkedInBot{}, p, "like_post", post2, "like")
		if err != nil {
			t.Fatal(err)
		}
		if m := res.(map[string]interface{}); m["already_reacted"] != true || m["current_reaction"] != "like" {
			t.Fatalf("res = %v", m)
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})
}
