//go:build social

package tiktok

import (
	"fmt"
	"strings"
	"testing"
)

func TestListUserVideosScrollsTheGrid(t *testing.T) {
	p := newPage(t)
	b := &TikTokBot{}
	res, err := call(t, b, p, "list_user_videos", profileURL("fake_creator"), float64(25))
	if err != nil {
		t.Fatal(err)
	}
	vids := resultList(t, res)
	if len(vids) != 25 {
		t.Fatalf("got %d videos, want 25 (lazy loading past the first 12)", len(vids))
	}
	seen := map[string]bool{}
	for i, v := range vids {
		wantID := fmt.Sprint(7200000000000000000 + int64(i+1))
		if v["id"] != wantID || v["url"] != "https://www.tiktok.com/@fake_creator/video/"+wantID {
			t.Fatalf("video %d = %v, want id %s", i, v, wantID)
		}
		if seen[v["url"].(string)] {
			t.Fatalf("duplicate %v", v["url"])
		}
		seen[v["url"].(string)] = true
	}
	if vids[0]["views"] != "100" || vids[0]["description"] != "Synthetic clip 1" {
		t.Fatalf("first video fields = %v", vids[0])
	}
}

func TestListUserVideosDefaultCountAndHandle(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "list_user_videos", "@fake_following", "")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(resultList(t, res)); n != 3 {
		t.Fatalf("got %d videos, want the profile's 3", n)
	}
}

func TestListUserVideosEmptyGridIsEmptyList(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "list_user_videos", profileURL("fake_friends"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if vids := resultList(t, res); vids == nil || len(vids) != 0 {
		t.Fatalf("got %v, want an empty (non-nil) list", vids)
	}
}

func TestListUserVideosPrivateProfileIsError(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "list_user_videos", profileURL("fake_private"), 10)
	if err == nil || !strings.Contains(err.Error(), "no video grid") {
		t.Fatalf("err = %v, want no video grid", err)
	}
}

func TestListUserVideosRequiresURL(t *testing.T) {
	p := newPage(t)
	if _, err := call(t, &TikTokBot{}, p, "list_user_videos", "", 5); err == nil || !strings.Contains(err.Error(), "profileURL") {
		t.Fatalf("err = %v", err)
	}
	// Wrong page type is rejected before anything happens.
	fn, _ := (&TikTokBot{}).GetMethodByName("list_user_videos")
	if _, err := fn(bg, "not-a-page", profileURL("x")); err == nil || !strings.Contains(err.Error(), "browser page") {
		t.Fatalf("err = %v", err)
	}
}

func TestListVideoCommentsOpensPanelAndReadsComments(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "list_video_comments", vidPlain, 3)
	if err != nil {
		t.Fatal(err)
	}
	cs := resultList(t, res)
	if len(cs) != 3 {
		t.Fatalf("got %d comments, want 3: %v", len(cs), cs)
	}
	want := []struct {
		user, name, text string
		liked            bool
	}{
		{"fake_ana", "Fake Ana", "Love the colors in this one", false},
		{"fake_ben", "Fake Ben", "First! great video", true},
		{"fake_cy", "Fake Cy", "Love the colors too", false},
	}
	for i, w := range want {
		c := cs[i]
		if c["username"] != w.user || c["displayName"] != w.name || c["text"] != w.text || c["liked"] != w.liked || c["videoUrl"] != vidPlain {
			t.Fatalf("comment %d = %v, want %+v", i, c, w)
		}
	}
	// The reply under Ben's comment is not a top-level comment and does not
	// leak into Ben's text.
	for _, c := range cs {
		if strings.Contains(c["text"].(string), "agree with ben") {
			t.Fatalf("reply leaked: %v", c)
		}
	}
	// The panel was opened with a trusted click and nothing else was clicked.
	wantEvents(t, p)
}

func TestListVideoCommentsScrollsForMore(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "list_video_comments", vidPlain, 10)
	if err != nil {
		t.Fatal(err)
	}
	cs := resultList(t, res)
	if len(cs) != 4 || cs[3]["username"] != "fake_eve" {
		t.Fatalf("got %v, want the 3 comments plus the lazily loaded one", cs)
	}
}

func TestListVideoCommentsTurnedOffIsError(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "list_video_comments", vidNoComments, 10)
	if err == nil || !strings.Contains(err.Error(), "comment button not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetProfileData(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "get_profile_data", profileURL("fake_creator"))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, res)
	want := map[string]interface{}{
		"username": "fake_creator", "handle": "fake_creator", "full_name": "Fake Creator",
		"bio": "Synthetic bio for tests only", "following_count": "87", "follower_count": "1234",
		"likes_count": "5.6K", "website": "https://example.test/fake-link", "is_verified": false,
		"profile_url": profileURL("fake_creator"),
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	if !strings.HasPrefix(fmt.Sprint(m["profile_picture_url"]), "data:image/gif") {
		t.Errorf("profile_picture_url = %v", m["profile_picture_url"])
	}
	// BotAdapter.GetProfileData reads the current page.
	d, err := (&TikTokBot{}).GetProfileData(bg, p)
	if err != nil || d["full_name"] != "Fake Creator" {
		t.Fatalf("GetProfileData = %v, %v", d, err)
	}
}

func TestGetProfileDataWithoutHeaderIsError(t *testing.T) {
	p := newPage(t)
	if err := p.Navigate(vidPlain); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, &TikTokBot{}, p, "get_profile_data"); err == nil || !strings.Contains(err.Error(), "no profile header") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchVideos(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "search_videos", "cats & dogs", 12)
	if err != nil {
		t.Fatal(err)
	}
	vs := resultList(t, res)
	if len(vs) != 12 {
		t.Fatalf("got %d results, want 12", len(vs))
	}
	first := vs[0]
	if first["url"] != "https://www.tiktok.com/@fake_maker_0/video/7300000000000000001" || first["author"] != "fake_maker_0" ||
		first["description"] != "Synthetic cats & dogs clip 1" || first["keyword"] != "cats & dogs" {
		t.Fatalf("first = %v", first)
	}
	for _, v := range vs {
		if strings.Contains(v["url"].(string), "fake_ad") {
			t.Fatalf("decoy outside the result cards was collected: %v", v)
		}
	}
}

func TestSearchVideosNoResultsIsError(t *testing.T) {
	p := newPage(t)
	if _, err := call(t, &TikTokBot{}, p, "search_videos", "nothing", 5); err == nil || !strings.Contains(err.Error(), "no video results") {
		t.Fatalf("err = %v", err)
	}
}

func TestListFollowers(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "list_followers", profileURL("fake_creator"), "FOLLOWERS_FETCH", 10)
	if err != nil {
		t.Fatal(err)
	}
	us := resultList(t, res)
	if len(us) != 10 {
		t.Fatalf("got %d, want 10 (6 + lazy loaded)", len(us))
	}
	if us[0]["username"] != "fake_followers_1" || us[0]["displayName"] != "Fake followers 1" || us[0]["source"] != "followers" {
		t.Fatalf("first = %v", us[0])
	}
	res, err = call(t, &TikTokBot{}, p, "list_followers", "fake_creator", "FOLLOWING_FETCH", 3)
	if err != nil {
		t.Fatal(err)
	}
	if us := resultList(t, res); len(us) != 3 || us[0]["username"] != "fake_following_1" {
		t.Fatalf("following = %v", us)
	}
}

func TestListFollowersPrivateIsError(t *testing.T) {
	p := newPage(t)
	if _, err := call(t, &TikTokBot{}, p, "list_followers", profileURL("fake_private"), "FOLLOWERS_FETCH", 5); err == nil || !strings.Contains(err.Error(), "empty or private") {
		t.Fatalf("err = %v", err)
	}
}

func TestListConversations(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "list_conversations", 10, true)
	if err != nil {
		t.Fatal(err)
	}
	cs := resultList(t, res)
	var names []string
	for _, c := range cs {
		names = append(names, c["name"].(string))
	}
	if strings.Join(names, ",") != "Fake Ana,Fake Cy,Fake Cy" {
		t.Fatalf("unread conversations = %v", names)
	}
	res, err = call(t, &TikTokBot{}, p, "list_conversations", 2, "false")
	if err != nil {
		t.Fatal(err)
	}
	if cs := resultList(t, res); len(cs) != 2 || cs[1]["name"] != "Fake Ben" || cs[1]["lastMessage"] != "thanks!" {
		t.Fatalf("all conversations = %v", cs)
	}
}

func TestIsLoggedIn(t *testing.T) {
	p := newPage(t)
	b := &TikTokBot{}
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"https://www.tiktok.com/foryou", true},
		{"https://www.tiktok.com/foryou?out=1", false},
	} {
		if err := p.Navigate(c.url); err != nil {
			t.Fatal(err)
		}
		got, err := b.IsLoggedIn(p)
		if err != nil || got != c.want {
			t.Fatalf("%s: IsLoggedIn = %v, %v; want %v", c.url, got, err, c.want)
		}
	}
}
