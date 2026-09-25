//go:build !nosocial

package x

// The shipped action JSONs (data/actions/x/*.json), run through the real
// action executor against the fixture pages, with this bot as the
// call_bot_method adapter — the same wiring BrowserNode uses.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/bot/bottest"
)

type flowRun struct {
	items []map[string]interface{}
	err   error
}

// runAction executes data/actions/x/<actionType>.json.
func runAction(t *testing.T, p *bottest.Page, actionType string, sa action.StorageAction, vars map[string]interface{}) flowRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ae := action.NewActionExecutor(ctx, p, nil, nil, nil, New(), zerolog.Nop())
	for k, v := range vars {
		ae.SetVariable(k, v)
	}
	sa.ID, sa.Type, sa.TargetPlatform = "flow-"+actionType, actionType, "X"
	res, err := ae.Execute(&sa)
	var items []map[string]interface{}
	if res != nil {
		items = res.ExtractedItems
	}
	return flowRun{items: items, err: err}
}

func column(items []map[string]interface{}, key string) []interface{} {
	var out []interface{}
	for _, it := range items {
		if v, ok := it[key]; ok {
			out = append(out, v)
		}
	}
	return out
}

func targets(urls ...string) []interface{} {
	var out []interface{}
	for _, u := range urls {
		out = append(out, map[string]interface{}{"url": u, "href": u, "username": u})
	}
	return out
}

func TestFlowReadActions(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)

	t.Run("scrape_profile_info", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "scrape_profile_info", action.StorageAction{
			Params: map[string]interface{}{"delayBetweenProfiles": 0.05},
		}, map[string]interface{}{"selectedListItems": targets("https://x.com/synth_alice", "https://x.com/synth_missing", "synth_dave")})
		if got := column(r.items, "username"); !reflect.DeepEqual(got, []interface{}{"synth_alice", "synth_dave"}) {
			t.Fatalf("usernames = %v (items %v)", got, r.items)
		}
		if r.items[0]["followers_count"] != int64(12500) || r.items[1]["followers_count"] != int64(60700000) {
			t.Errorf("counts: %v / %v", r.items[0]["followers_count"], r.items[1]["followers_count"])
		}
		// The missing profile is a recorded failure, not a silent skip.
		wantErr(t, r.err, "get_profile_data")
	})

	t.Run("find_by_keyword", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "find_by_keyword", action.StorageAction{
			Keywords: "synthkeyword",
			Params:   map[string]interface{}{"maxResultsCount": 3},
		}, nil)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if got := column(r.items, "id"); !reflect.DeepEqual(got, []interface{}{"2001", "2002", "2003"}) {
			t.Fatalf("ids = %v", got)
		}
	})

	t.Run("find_by_keyword needs a keyword", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "find_by_keyword", action.StorageAction{}, nil)
		wantErr(t, r.err, "keyword")
	})

	t.Run("export_followers", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "export_followers", action.StorageAction{
			Params: map[string]interface{}{"target_url": "https://x.com/synth_alice", "sourceType": "FOLLOWING_FETCH", "maxResultsCount": 10},
		}, nil)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if got := column(r.items, "username"); !reflect.DeepEqual(got, []interface{}{"synth_g1", "synth_g2"}) {
			t.Fatalf("usernames = %v", got)
		}
	})

	t.Run("export_followers defaults to followers", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "export_followers", action.StorageAction{
			Params: map[string]interface{}{"targetUsername": "synth_alice", "maxResultsCount": 4},
		}, nil)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if got := column(r.items, "username"); !reflect.DeepEqual(got, []interface{}{"synth_f1", "synth_f2", "synth_f3", "synth_f4"}) {
			t.Fatalf("usernames = %v", got)
		}
	})

	t.Run("export_followers without a target fails", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "export_followers", action.StorageAction{}, nil)
		wantErr(t, r.err, "profile")
	})
}

func TestFlowWriteActions(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)

	t.Run("engage_with_posts: like and reply given posts", func(t *testing.T) {
		p := xPage(t, b)
		r := runAction(t, p, "engage_with_posts", action.StorageAction{
			ContentMessage: "flow reply",
			Params:         map[string]interface{}{"delayBetweenPosts": 0.05},
		}, map[string]interface{}{"selectedListItems": targets(statusURL("1001"))})
		if r.err != nil {
			t.Fatal(r.err)
		}
		if got := column(r.items, "liked"); !reflect.DeepEqual(got, []interface{}{true}) {
			t.Errorf("liked = %v (items %v)", got, r.items)
		}
		if got := column(r.items, "replied"); !reflect.DeepEqual(got, []interface{}{true}) {
			t.Errorf("replied = %v", got)
		}
		// reply_post reloaded the post page; the reply is its last write.
		if r := js(t, p, `JSON.stringify(window.__replies)`); r != `["flow reply"]` {
			t.Errorf("replies posted = %v", r)
		}
	})

	t.Run("engage_with_posts: already liked, like only", func(t *testing.T) {
		p := xPage(t, b)
		r := runAction(t, p, "engage_with_posts", action.StorageAction{
			Params: map[string]interface{}{"delayBetweenPosts": 0.05},
		}, map[string]interface{}{"selectedListItems": []interface{}{statusURL("1003")}})
		if r.err != nil {
			t.Fatal(r.err)
		}
		if got := column(r.items, "already_liked"); !reflect.DeepEqual(got, []interface{}{true}) {
			t.Errorf("already_liked = %v", got)
		}
		if got := column(r.items, "replied"); got != nil {
			t.Errorf("replied without commentText: %v", got)
		}
	})

	t.Run("engage_with_posts: from a keyword search", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "engage_with_posts", action.StorageAction{
			Keywords: "synthkeyword",
			Params:   map[string]interface{}{"maxContentCount": 1, "delayBetweenPosts": 0.05},
		}, nil)
		if r.err != nil {
			t.Fatal(r.err)
		}
		var liked []interface{}
		for _, it := range r.items {
			if _, ok := it["liked"]; ok && it["url"] == "https://x.com/synth_erin/status/2001" && it["already_liked"] != nil {
				liked = append(liked, it["liked"])
			}
		}
		if !reflect.DeepEqual(liked, []interface{}{true}) {
			t.Fatalf("like result for the found post missing: %v", r.items)
		}
	})

	t.Run("engage_with_posts: like fails, run fails", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "engage_with_posts", action.StorageAction{
			Params: map[string]interface{}{"delayBetweenPosts": 0.05},
		}, map[string]interface{}{"selectedListItems": targets(statusURL("1005"))})
		wantErr(t, r.err, "not confirmed")
	})

	t.Run("send_dms", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "send_dms", action.StorageAction{
			ContentMessage: "flow hello",
			Params:         map[string]interface{}{"delayBetweenMessages": 0.05},
		}, map[string]interface{}{"selectedListItems": targets("https://x.com/synth_alice", "https://x.com/synth_bob")})
		if got := column(r.items, "recipient"); !reflect.DeepEqual(got, []interface{}{"synth_alice"}) {
			t.Errorf("sent to %v", got)
		}
		wantErr(t, r.err, "cannot be messaged")
	})

	t.Run("auto_reply_dms", func(t *testing.T) {
		p := xPage(t, b)
		r := runAction(t, p, "auto_reply_dms", action.StorageAction{
			Params: map[string]interface{}{"replyText": "auto synthetic reply", "delayBetweenReplies": 0.05},
		}, nil)
		if r.err != nil {
			t.Fatal(r.err)
		}
		want := []interface{}{"https://x.com/messages/100-200", "https://x.com/messages/100-500"}
		if got := column(r.items, "conversation_url"); !reflect.DeepEqual(got, want) {
			t.Fatalf("replied in %v, want %v", got, want)
		}
		for _, it := range r.items {
			if it["conversation_url"] != nil && it["sent"] != true {
				t.Errorf("row %v", it)
			}
		}
	})

	t.Run("auto_reply_dms needs replyText", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "auto_reply_dms", action.StorageAction{}, nil)
		wantErr(t, r.err, "replyText")
	})

	t.Run("publish_post", func(t *testing.T) {
		p := xPage(t, b)
		r := runAction(t, p, "publish_post", action.StorageAction{ContentMessage: "flow post"}, nil)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if got := column(r.items, "post_url"); !reflect.DeepEqual(got, []interface{}{"https://x.com/synth_me/status/5001"}) {
			t.Errorf("post_url = %v", got)
		}
		if s := js(t, p, `JSON.stringify(window.__posts)`); s != `[{"text":"flow post","files":0}]` {
			t.Errorf("posts = %v", s)
		}
	})

	t.Run("publish_post refused", func(t *testing.T) {
		r := runAction(t, xPage(t, b), "publish_post", action.StorageAction{ContentMessage: "flow dup"}, nil)
		wantErr(t, r.err, "You already said that")
	})
}
