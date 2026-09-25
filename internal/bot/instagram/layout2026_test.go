//go:build social

package instagram

// Regressions found by the live read-only run against instagram.com
// (2026-09-25): the 2026 profile header and post page. Fixtures are
// synthetic (testdata/post_2026.html, profile.html with FX.layout2026).

import (
	"reflect"
	"strings"
	"testing"
)

// Your own profile: the followers count is <a role="link" href="#"> outside
// any <ul>/<li>, and /<you>/followers/ does not open the dialog.
func TestFetchFollowersListOwnProfile2026Header(t *testing.T) {
	s := newSite()
	s.profiles["fake.me"] = fx{"layout2026": true, "noRoute": true, "followers": 12}
	page, _ := s.open(t)
	res, err := call(t, page, "fetch_followers_list", "https://www.instagram.com/fake.me/", "FOLLOWERS_FETCH", 5)
	if err != nil {
		t.Fatal(err)
	}
	got := resultList(t, res)
	if len(got) != 5 || got[0]["username"] != "fake.f01" || got[0]["source_profile"] != "fake.me" {
		t.Fatalf("followers = %v", got)
	}
	res, err = call(t, page, "fetch_followers_list", "fake.me", "following", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultList(t, res); len(got) != 3 || got[0]["username"] != "fake.g01" {
		t.Fatalf("following = %v", got)
	}
}

// Rendered header counts win over a stale og:description, and the Threads
// badge is not the website.
func TestGetUserInfo2026HeaderCountsAndWebsite(t *testing.T) {
	s := newSite() // web_profile_info answers 404 → the rendered page
	s.profiles["fake.me"] = fx{"layout2026": true, "hdrFollowing": "50", "hdrPosts": "13", "hdrFollowers": "1,240", "hdrFollowersExact": "1,240"}
	page, _ := s.open(t)
	res, err := call(t, page, "get_user_info", "https://www.instagram.com/fake.me/")
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, res)
	want := map[string]interface{}{
		"username": "fake.me", "full_name": "Ada Fixture", "introduction": "Synthetic bio used only by tests",
		"follower_count": "1240", "following_count": "50", "content_count": "13", "website": "example.test",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %#v, want %#v", k, m[k], v)
		}
	}
}

func post2026Site() *site {
	s := newSite()
	s.postPage = "post_2026.html"
	s.post = fx{"comments": []fx{
		{"id": "201", "author": "fake.c1", "text": "Synthetic first comment", "time": "2026-09-25T11:43:20.000Z", "ago": "21 s"},
		{"id": "202", "author": "fake.c2", "text": "🤣😁", "time": "2026-09-25T11:43:17.000Z", "ago": "22 s"},
		{"id": "203", "author": "fake.c3", "text": "ok", "time": "2026-09-25T11:42:39.000Z", "ago": "1 m"},
		{"id": "204", "author": "fake.c4", "text": "love", "time": "2026-09-25T11:40:00.000Z", "ago": "3 m"},
	}}
	return s
}

// Comment text is the body span, never the relative-time label "22 s"; an
// emoji-only comment survives.
func TestListPostComments2026RelativeTimeIsNotText(t *testing.T) {
	page, _ := post2026Site().open(t)
	res, err := call(t, page, "list_post_comments", "https://www.instagram.com/p/FXCAR26/", 10)
	if err != nil {
		t.Fatal(err)
	}
	cs := resultList(t, res)
	var got []string
	for _, c := range cs {
		got = append(got, c["author"].(string)+"="+c["text"].(string))
	}
	want := []string{"fake.c1=Synthetic first comment", "fake.c2=🤣😁", "fake.c3=ok", "fake.c4=love"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comments = %q, want %q", got, want)
	}
}

// The 2026 post page: author from the post (not the first @mention of the
// caption), the caption and its mentions, only the post's own media (no
// "more posts" thumbnails, no blob: URLs), and carousel — the same answer
// the logged-out page gives.
func TestScrapePostData2026(t *testing.T) {
	for _, embed := range []bool{true, false} {
		s := post2026Site()
		if !embed {
			s.post["noEmbed"] = true
		}
		page, _ := s.open(t)
		res, err := call(t, page, "scrape_post_data", "https://www.instagram.com/p/FXCAR26/")
		if err != nil {
			t.Fatal(err)
		}
		m := resultMap(t, res)
		if m["author_username"] != "fake.author" || m["author_url"] != "https://www.instagram.com/fake.author/" {
			t.Errorf("embed=%v: author = %v (%v)", embed, m["author_username"], m["author_url"])
		}
		caption, _ := m["caption"].(string)
		if !strings.HasPrefix(caption, "Three synthetic clips, all filmed on @fake.brand") || !strings.HasSuffix(caption, "Clips by @fake.one and @fake.two") {
			t.Errorf("embed=%v: caption = %q", embed, caption)
		}
		if got := m["mentions"]; !reflect.DeepEqual(got, []interface{}{"fake.brand", "fake.one", "fake.two"}) {
			t.Errorf("embed=%v: mentions = %#v", embed, got)
		}
		media, _ := m["media_urls"].([]interface{})
		for _, u := range media {
			if s := u.(string); strings.HasPrefix(s, "blob:") || strings.Contains(s, "more") || strings.Contains(s, "avatar") {
				t.Errorf("embed=%v: media_urls has %q (not the post's own media)", embed, s)
			}
		}
		if embed {
			want := []interface{}{"https://scontent.fixture.invalid/clip1.mp4", "https://scontent.fixture.invalid/clip2.mp4", "https://scontent.fixture.invalid/clip3.mp4"}
			if !reflect.DeepEqual(media, want) {
				t.Errorf("media_urls = %#v, want %#v", media, want)
			}
			if m["post_type"] != "carousel" {
				t.Errorf("post_type = %v, want carousel", m["post_type"])
			}
			if m["likes_count"] != "4321" || m["comments_count"] != "98" {
				t.Errorf("likes/comments = %v/%v", m["likes_count"], m["comments_count"])
			}
		}
		if m["post_date"] != "2026-09-20T10:00:00.000Z" {
			t.Errorf("embed=%v: post_date = %v", embed, m["post_date"])
		}
	}
}
