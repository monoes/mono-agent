//go:build !nosocial

package instagram

import (
	"fmt"
	"strings"
	"testing"
)

func TestGetUserInfoFromProfileEndpoint(t *testing.T) {
	s := newSite()
	s.profileAPI = true
	page, rec := s.open(t)
	res, err := call(t, page, "get_user_info", "https://www.instagram.com/fake.ada/")
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, res)
	want := map[string]interface{}{
		"username": "fake.ada", "full_name": "Ada Fixture (API)", "introduction": "Synthetic API bio",
		"follower_count": "1234", "following_count": "56", "content_count": "12", "is_verified": true,
		"website": "https://example.test/", "url": "https://www.instagram.com/fake.ada/",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %#v, want %#v", k, m[k], v)
		}
	}
	if len(rec.Matching("GET", "*web_profile_info/?username=fake.ada")) == 0 {
		t.Errorf("profile endpoint not called: %v", rec.Requests())
	}
}

func TestGetUserInfoFallsBackToRenderedProfile(t *testing.T) {
	s := newSite() // web_profile_info answers 404
	page, _ := s.open(t)
	res, err := call(t, page, "get_user_info", "@fake.ada")
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, res)
	want := map[string]interface{}{
		"username": "fake.ada", "full_name": "Ada Fixture", "introduction": "Synthetic bio used only by tests",
		"follower_count": "1234", "following_count": "56", "content_count": "12", "is_verified": true,
		"website": "https://example.test/", "is_private": false,
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %#v, want %#v", k, m[k], v)
		}
	}
}

func TestGetUserInfoRejectsEmptyTarget(t *testing.T) {
	s := newSite()
	page, _ := s.open(t)
	if _, err := call(t, page, "get_user_info", "https://www.instagram.com/p/FXPOST01/"); err == nil || !strings.Contains(err.Error(), "could not determine username") {
		t.Fatalf("a post URL is not a profile: err = %v", err)
	}
}

func TestExtractUsernameFromMetadata(t *testing.T) {
	s := newSite()
	page, _ := s.open(t)
	for _, c := range []struct{ url, want string }{
		{"https://www.instagram.com/fake.ada/", "fake.ada"},
		// A post page: og:url is /p/<code>/ — the shortcode is not a
		// username; the author comes from the post header.
		{"https://www.instagram.com/p/FXPOST01/", "fake.author"},
		{"https://www.instagram.com/fake.author/p/FXPOST02/", "fake.author"},
	} {
		if err := page.Navigate(c.url); err != nil {
			t.Fatal(err)
		}
		res, err := call(t, page, "extract_username_from_metadata")
		if err != nil {
			t.Fatalf("%s: %v", c.url, err)
		}
		if res != c.want {
			t.Errorf("%s: username = %v, want %s", c.url, res, c.want)
		}
	}
}

func TestFetchFollowersListScrollsTheDialog(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "fetch_followers_list", "https://www.instagram.com/fake.ada/", "FOLLOWERS_FETCH", 15)
	if err != nil {
		t.Fatal(err)
	}
	users := resultList(t, res)
	if len(users) != 15 {
		t.Fatalf("got %d users, want 15 (needs one dialog scroll)", len(users))
	}
	for i, u := range users {
		want := fmt.Sprintf("fake.f%02d", i+1)
		if u["username"] != want || u["url"] != "https://www.instagram.com/"+want+"/" || u["full_name"] != fmt.Sprintf("Person %d", i+1) {
			t.Errorf("user %d = %v, want %s", i, u, want)
		}
	}
	if p := apiPosts(rec); len(p) != 0 {
		t.Errorf("a read made writes: %v", p)
	}
}

func TestFetchFollowersListAllAndFollowing(t *testing.T) {
	s := newSite()
	page, _ := s.open(t)
	// More requested than exist: stops when the list stops growing.
	res, err := call(t, page, "fetch_followers_list", "", "https://www.instagram.com/fake.ada/", 100)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(resultList(t, res)); n != 25 {
		t.Fatalf("got %d followers, want all 25", n)
	}
	res, err = call(t, page, "fetch_followers_list", "fake.ada", "following", 10)
	if err != nil {
		t.Fatal(err)
	}
	users := resultList(t, res)
	if len(users) != 3 || users[0]["username"] != "fake.g01" || users[0]["source"] != "following" {
		t.Fatalf("following = %v", users)
	}
}

func TestFetchFollowersListNeedsAProfile(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	// export_followers' template fallback passes sourceType as the profile.
	if _, err := call(t, page, "fetch_followers_list", "FOLLOWERS_FETCH", "FOLLOWERS_FETCH", 10); err == nil || !strings.Contains(err.Error(), "no profile") {
		t.Fatalf("err = %v", err)
	}
	if n := len(rec.Requests()); n != 0 {
		t.Errorf("opened pages without a profile: %v", rec.Requests())
	}
}

func TestFetchFollowersListPrivateProfileFails(t *testing.T) {
	s := newSite()
	s.profiles["fake.priv"] = fx{"state": "follow", "private": true}
	page, _ := s.open(t)
	if _, err := call(t, page, "fetch_followers_list", "fake.priv", "followers", 10); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("err = %v, want a private-account error", err)
	}
}

func TestListUserPosts(t *testing.T) {
	s := newSite()
	page, _ := s.open(t)
	res, err := call(t, page, "list_user_posts", "fake.ada", 8)
	if err != nil {
		t.Fatal(err)
	}
	posts := resultList(t, res)
	if len(posts) != 8 {
		t.Fatalf("got %d posts, want 8 (needs a scroll)", len(posts))
	}
	if posts[0]["url"] != "https://www.instagram.com/fake.ada/p/FXPOST01/" || posts[0]["shortcode"] != "FXPOST01" || posts[0]["author"] != "fake.ada" {
		t.Errorf("first post = %v", posts[0])
	}
	if posts[3]["type"] != "reel" || posts[3]["url"] != "https://www.instagram.com/fake.ada/reel/FXPOST04/" {
		t.Errorf("fourth post = %v", posts[3])
	}
	for _, p := range posts {
		if strings.Contains(p["url"].(string), "highlights") {
			t.Errorf("a highlight was listed as a post: %v", p)
		}
	}
}

func TestListUserPostsPrivate(t *testing.T) {
	s := newSite()
	s.profiles["fake.priv"] = fx{"state": "follow", "private": true}
	page, _ := s.open(t)
	if _, err := call(t, page, "list_user_posts", "fake.priv", 5); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchPosts(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "search_posts", "#fixture", 12)
	if err != nil {
		t.Fatal(err)
	}
	posts := resultList(t, res)
	if len(posts) != 12 {
		t.Fatalf("got %d posts, want 12", len(posts))
	}
	if posts[0]["url"] != "https://www.instagram.com/p/FXS001/" {
		t.Errorf("first = %v", posts[0])
	}
	if posts[2]["author"] != "fake.tagger3" || posts[4]["type"] != "reel" {
		t.Errorf("third/fifth = %v / %v", posts[2], posts[4])
	}
	if len(rec.Matching("GET", "https://www.instagram.com/explore/tags/fixture/")) == 0 {
		t.Errorf("hashtag page not opened: %v", rec.Requests())
	}
}

func TestSearchPostsMultiWordUsesKeywordSearch(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "search_posts", "synthetic fixtures", 3)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(resultList(t, res)); n != 3 {
		t.Fatalf("got %d posts", n)
	}
	if len(rec.Matching("GET", "https://www.instagram.com/explore/search/keyword/?q=synthetic+fixtures")) == 0 {
		t.Errorf("keyword search not opened: %v", rec.Requests())
	}
}

func TestScrapePostData(t *testing.T) {
	s := newSite()
	page, _ := s.open(t)
	res, err := call(t, page, "scrape_post_data", "https://www.instagram.com/p/FXPOST01/")
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, res)
	want := map[string]interface{}{
		"author_username": "fake.author", "shortcode": "FXPOST01", "post_url": "https://www.instagram.com/p/FXPOST01/",
		"caption": "A synthetic caption for tests #fixture #synthetic with @fake.c1", "likes_count": "1234",
		"post_date": "2026-09-20T10:00:00.000Z", "post_type": "image", "is_liked": false, "comments_count": "7",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %#v, want %#v", k, m[k], v)
		}
	}
	if media, _ := m["media_urls"].([]interface{}); len(media) != 1 || media[0] != "https://scontent.fixture.invalid/p1.jpg" {
		t.Errorf("media_urls = %#v", m["media_urls"])
	}
	if tags, _ := m["hashtags"].([]interface{}); len(tags) != 2 {
		t.Errorf("hashtags = %#v", m["hashtags"])
	}
	if _, has := m["comments"]; has {
		t.Error("comments returned without includeComments")
	}
}

func TestScrapePostDataCarouselReelAndComments(t *testing.T) {
	s := newSite()
	s.post["kind"] = "carousel"
	s.post["liked"] = true
	page, _ := s.open(t)
	res, err := call(t, page, "scrape_post_data", "https://www.instagram.com/p/FXCAR01/", "true", 2)
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, res)
	if m["post_type"] != "carousel" || m["is_liked"] != true {
		t.Errorf("post_type = %v, is_liked = %v", m["post_type"], m["is_liked"])
	}
	if media, _ := m["media_urls"].([]interface{}); len(media) != 2 {
		t.Errorf("media_urls = %#v", m["media_urls"])
	}
	if cs, _ := m["comments"].([]interface{}); len(cs) != 2 {
		t.Errorf("comments = %#v", m["comments"])
	}
	res, err = call(t, page, "scrape_post_data", "https://www.instagram.com/reel/FXREEL1/")
	if err != nil {
		t.Fatal(err)
	}
	m = resultMap(t, res)
	if m["post_type"] != "reel" || m["post_url"] != "https://www.instagram.com/reel/FXREEL1/" {
		t.Errorf("reel: %v", m)
	}
	if media, _ := m["media_urls"].([]interface{}); len(media) != 1 || media[0] != "https://scontent.fixture.invalid/reel.mp4" {
		t.Errorf("reel media_urls = %#v", m["media_urls"])
	}
}

func TestListPostComments(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "list_post_comments", "https://www.instagram.com/p/FXPOST01/", 50)
	if err != nil {
		t.Fatal(err)
	}
	cs := resultList(t, res)
	type row struct {
		author, text string
		liked, reply bool
		likes        int
	}
	want := []row{
		{"fake.c1", "First synthetic comment", false, false, 2},
		{"fake.c2", "Second synthetic comment", true, false, 0},
		{"fake.c3", "A synthetic reply to c2", false, true, 0},
		{"fake.author", "Thanks everyone", false, false, 0},
		{"fake.late", "A comment behind load more", false, false, 0},
	}
	if len(cs) != len(want) {
		t.Fatalf("got %d comments: %v", len(cs), cs)
	}
	for i, w := range want {
		c := cs[i]
		if c["author"] != w.author || c["text"] != w.text || c["is_liked"] != w.liked || c["is_reply"] != w.reply || c["likes_count"] != w.likes {
			t.Errorf("comment %d = %v, want %+v", i, c, w)
		}
		if c["timestamp"] == "" {
			t.Errorf("comment %d has no timestamp", i)
		}
	}
	// Expanding "Load more comments" is a read; liking/commenting is not.
	for _, p := range apiPosts(rec) {
		if strings.Contains(p, "/comments/") || strings.Contains(p, "/likes/") {
			t.Errorf("a read made a write: %s", p)
		}
	}
	// maxCount caps the list.
	res, err = call(t, page, "list_post_comments", "https://www.instagram.com/p/FXPOST01/", 2)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(resultList(t, res)); n != 2 {
		t.Errorf("maxCount 2: got %d", n)
	}
}

func TestGetProfileDataAndIsLoggedIn(t *testing.T) {
	s := newSite()
	page, _ := s.open(t)
	if err := page.Navigate("https://www.instagram.com/fake.ada/"); err != nil {
		t.Fatal(err)
	}
	b := New()
	in, err := b.IsLoggedIn(page)
	if err != nil || !in {
		t.Fatalf("IsLoggedIn = %v, %v", in, err)
	}
	m, err := b.GetProfileData(t.Context(), page)
	if err != nil {
		t.Fatal(err)
	}
	if m["username"] != "fake.ada" || m["follower_count"] != "1234" {
		t.Errorf("GetProfileData = %v", m)
	}
}
