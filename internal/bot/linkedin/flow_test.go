//go:build !nosocial

package linkedin

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/bot/bottest"
)

// runAction runs data/actions/linkedin/<actionType>.json through the real
// action executor against a fixture page, the way BrowserNode does
// (selectedListItems seeded as a variable, node message as ContentMessage).
func runAction(t *testing.T, p *bottest.Page, actionType, message string, params map[string]interface{}, items ...interface{}) (*action.ExecutionResult, error) {
	t.Helper()
	ae := action.NewActionExecutor(context.Background(), p, nil, nil, nil, &LinkedInBot{}, zerolog.Nop())
	if len(items) > 0 {
		ae.SetVariable("selectedListItems", items)
	}
	res, err := ae.Execute(&action.StorageAction{ID: "test-" + actionType, Type: actionType, TargetPlatform: "linkedin", ContentMessage: message, Params: params})
	if err == nil {
		assertOutputsDeclared(t, actionType, res)
	}
	return res, err
}

// target mimics BrowserNode's wrapping of a string target.
func target(s string) map[string]interface{} {
	return map[string]interface{}{"url": s, "href": s, "username": s}
}

func TestActionFlows(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)

	t.Run("send_dms sends to messageable members and fails the rest without connecting", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		res, err := runAction(t, p, "send_dms", "Hello from the flow test", map[string]interface{}{"delayBetweenMessages": 0.05},
			target("https://www.linkedin.com/in/ada-first-test/"), target("https://www.linkedin.com/in/cy-closed-test/"), target("bo-third-test"))
		if err == nil || !strings.Contains(err.Error(), "no Message option") {
			t.Fatalf("err = %v, want the closed profile reported", err)
		}
		if len(res.FailedItems) != 1 {
			t.Fatalf("failed = %v", res.FailedItems)
		}
		onlySends(t, rec, "Hello from the flow test", "Ada First", "Bo Third")
	})

	t.Run("send_dms requires a message", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		if _, err := runAction(t, p, "send_dms", "", nil, target("ada-first-test")); err == nil || !strings.Contains(err.Error(), "messageText") {
			t.Fatalf("err = %v", err)
		}
		if len(rec.Requests()) != 0 {
			t.Fatal("ran without a message")
		}
	})

	t.Run("auto_reply_dms answers conversations waiting for a reply", func(t *testing.T) {
		p, rec := newPage(t, b, messagingRoute)
		res, err := runAction(t, p, "auto_reply_dms", "Thanks, I'll get back to you soon.", map[string]interface{}{"delayBetweenReplies": 0.05})
		if err != nil {
			t.Fatal(err)
		}
		onlySends(t, rec, "Thanks, I'll get back to you soon.", "Gia Unread", "Jon Waiting")
		if len(res.ExtractedItems) == 0 {
			t.Fatal("no output items")
		}
	})

	t.Run("engage_with_posts reacts and comments on each found post", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute, bottest.Route{Pattern: "https://www.linkedin.com/search/results/content/*", File: "testdata/activity.html"})
		_, err := runAction(t, p, "engage_with_posts", "", map[string]interface{}{
			"keyword": "sprockets", "maxContentCount": 2, "commentText": "Nice one", "reaction": "celebrate", "delayBetweenPosts": 0.05,
		})
		if err != nil {
			t.Fatal(err)
		}
		var likes, comments int
		for _, w := range writes(rec) {
			switch {
			case strings.Contains(w, `"target":"post","type":"CELEBRATE"`):
				likes++
			case strings.HasPrefix(w, "comments?action=create") && strings.Contains(w, "Nice one"):
				comments++
			default:
				t.Errorf("unexpected write %q", w)
			}
		}
		if likes != 2 || comments != 2 {
			t.Fatalf("likes=%d comments=%d, want 2 and 2", likes, comments)
		}
		if n := len(rec.Matching("GET", "https://www.linkedin.com/feed/update/urn:li:activity:73000000000000000*")); n != 4 {
			t.Fatalf("post visits = %d, want 4 (2 posts × react + comment)", n)
		}
	})

	t.Run("engage_with_posts with reaction none and no comment only reads", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute, bottest.Route{Pattern: "https://www.linkedin.com/search/results/content/*", File: "testdata/activity.html"})
		if _, err := runAction(t, p, "engage_with_posts", "", map[string]interface{}{
			"searches": []interface{}{map[string]interface{}{"keyword": "sprockets"}}, "maxContentCount": 1, "reaction": "none", "delayBetweenPosts": 0.05,
		}); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("engage_with_posts on given post URLs does not search", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := runAction(t, p, "engage_with_posts", "", map[string]interface{}{"delayBetweenPosts": 0.05}, target(testPost)); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], `"type":"LIKE"`) {
			t.Fatalf("writes = %v", w)
		}
		if len(rec.Matching("GET", "https://www.linkedin.com/search/*")) != 0 {
			t.Fatal("searched although posts were given")
		}
	})

	t.Run("like_posts", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := runAction(t, p, "like_posts", "", map[string]interface{}{"reaction": "love", "delayBetweenLikes": 0.05}, target(testPost)); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], `"type":"LOVE"`) {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("like_posts reports an unconfirmed like", func(t *testing.T) {
		p, _ := newPage(t, b, postRoute)
		if _, err := runAction(t, p, "like_posts", "", map[string]interface{}{"delayBetweenLikes": 0.05}, target(testPost+"?broken=react")); err == nil {
			t.Fatal("want the failed like reported")
		}
	})

	t.Run("comment_on_posts", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := runAction(t, p, "comment_on_posts", "Flow comment", map[string]interface{}{"delayBetweenComments": 0.05}, target(testPost)); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], "Flow comment") {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("like_comments with string comment ids", func(t *testing.T) {
		p, rec := newPage(t, b, postRoute)
		if _, err := runAction(t, p, "like_comments", "", map[string]interface{}{"postUrl": testPost, "delayBetweenLikes": 0.05}, target(commentA)); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], "7100000000000000101") {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("list_post_comments", func(t *testing.T) {
		p, _ := newPage(t, b, postRoute)
		res, err := runAction(t, p, "list_post_comments", "", map[string]interface{}{"includeReplies": false, "delayBetweenPosts": 0.05}, target(testPost))
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, it := range res.ExtractedItems {
			if id, ok := it["id"].(string); ok {
				ids = append(ids, id)
			}
		}
		if strings.Join(ids, ",") != strings.Join([]string{commentA, commentB, lateComment}, ",") {
			t.Fatalf("ids = %v", ids)
		}
	})

	t.Run("list_user_posts", func(t *testing.T) {
		p, _ := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/company/*", File: "testdata/activity.html"})
		items := []interface{}{target("https://www.linkedin.com/company/example-labs-test/")}
		res, err := runAction(t, p, "list_user_posts", "", map[string]interface{}{"targets": items, "maxCount": 3}, items...)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(res.ExtractedItems); n != 3 {
			t.Fatalf("items = %d: %v", n, res.ExtractedItems)
		}
	})

	t.Run("scrape_profile_info", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		res, err := runAction(t, p, "scrape_profile_info", "", map[string]interface{}{"delayBetweenProfiles": 0.05},
			target("https://www.linkedin.com/in/ada-first-test/"), target("bo-third-test"))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, it := range res.ExtractedItems {
			names = append(names, it["full_name"].(string))
		}
		if strings.Join(names, ",") != "Ada First,Bo Third" {
			t.Fatalf("names = %v", names)
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("find_by_keyword", func(t *testing.T) {
		p, _ := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/search/results/people/*", File: "testdata/search_people.html"})
		res, err := runAction(t, p, "find_by_keyword", "", map[string]interface{}{"maxResultsCount": 4, "keyword": "workflow automation"})
		if err != nil {
			t.Fatal(err)
		}
		if n := len(res.ExtractedItems); n != 4 {
			t.Fatalf("items = %d", n)
		}
		if res.ExtractedItems[0]["profile_url"] != "https://www.linkedin.com/in/lena-ortiz-test/" {
			t.Fatalf("first = %v", res.ExtractedItems[0])
		}
	})

	t.Run("export_followers", func(t *testing.T) {
		p, rec := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/mynetwork/network-manager/people-follow/*", File: "testdata/followers.html"})
		res, err := runAction(t, p, "export_followers", "", map[string]interface{}{"sourceType": "FOLLOWERS_FETCH", "maxResultsCount": 4})
		if err != nil {
			t.Fatal(err)
		}
		if n := len(res.ExtractedItems); n != 4 {
			t.Fatalf("items = %d", n)
		}
		if len(rec.Matching("POST", "*/follow")) != 0 {
			t.Fatal("a follow button was pressed")
		}
	})

	t.Run("publish_post", func(t *testing.T) {
		p, rec := newPage(t, b, shareRoute)
		if _, err := runAction(t, p, "publish_post", "", map[string]interface{}{"text": "Flow post"}); err != nil {
			t.Fatal(err)
		}
		if w := writes(rec); len(w) != 1 || !strings.Contains(w[0], "Flow post") {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("publish_post without media is valid", func(t *testing.T) {
		if err := action.ValidateActionInputs("linkedin", "publish_post", &action.StorageAction{Params: map[string]interface{}{"text": "x"}}, nil); err != nil {
			t.Fatal(err)
		}
	})
}
