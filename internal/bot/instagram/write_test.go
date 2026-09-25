//go:build social

package instagram

import (
	"net/url"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

const postURL = "https://www.instagram.com/p/FXPOST01/"

func form(t *testing.T, r bottest.Request) url.Values {
	t.Helper()
	v, err := url.ParseQuery(r.PostData)
	if err != nil {
		t.Fatalf("post body %q: %v", r.PostData, err)
	}
	return v
}

// ---------------------------------------------------------------------------
// like_post
// ---------------------------------------------------------------------------

func TestLikePostLikesThePostNotAComment(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "like_post", postURL)
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "liked" || m["success"] != true {
		t.Errorf("result = %v", m)
	}
	if got := posts(rec, "*/api/v1/web/likes/FXMEDIA1/like/"); len(got) != 1 {
		t.Errorf("post like requests = %d, want 1; all: %v", len(got), apiPosts(rec))
	}
	if got := posts(rec, "*/comments/*"); len(got) != 0 {
		t.Errorf("a comment was touched: %v", apiPosts(rec))
	}
}

func TestLikePostAlreadyLikedIsLeftAlone(t *testing.T) {
	s := newSite()
	s.post["liked"] = true
	page, rec := s.open(t)
	res, err := call(t, page, "like_post", postURL)
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "already_liked" {
		t.Errorf("status = %v", m["status"])
	}
	if p := apiPosts(rec); len(p) != 0 {
		t.Errorf("an already-liked post must not be clicked (Unlike!): %v", p)
	}
}

func TestLikePostUnconfirmedIsAnError(t *testing.T) {
	s := newSite()
	s.post["noconfirm"] = true
	page, rec := s.open(t)
	_, err := call(t, page, "like_post", postURL)
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v, want an unconfirmed-like error", err)
	}
	if got := posts(rec, "*/likes/FXMEDIA1/like/"); len(got) != 1 {
		t.Errorf("exactly one like attempt expected, got %v", apiPosts(rec))
	}
}

func TestLikePostRejectsNonPostURL(t *testing.T) {
	s := newSite()
	page, _ := s.open(t)
	if _, err := call(t, page, "like_post", "https://www.instagram.com/fake.ada/"); err == nil {
		t.Fatal("a profile URL is not a post")
	}
}

// ---------------------------------------------------------------------------
// comment_post
// ---------------------------------------------------------------------------

func TestCommentPostPostsAndVerifies(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	text := "Lovely synthetic shot ✨ 👍"
	res, err := call(t, page, "comment_post", postURL, text)
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, res)
	if m["status"] != "commented" || m["verified_by"] != "comment_rendered" {
		t.Errorf("result = %v", m)
	}
	got := posts(rec, "*/api/v1/web/comments/FXMEDIA1/add/")
	if len(got) != 1 {
		t.Fatalf("add-comment requests = %d: %v", len(got), apiPosts(rec))
	}
	if f := form(t, got[0]); f.Get("comment_text") != text || f.Get("replied_to_comment_id") != "" {
		t.Errorf("posted %v", f)
	}
}

func TestCommentPostFailures(t *testing.T) {
	for name, opt := range map[string]fx{
		"no confirmation": {"noconfirm": true},
		"error toast":     {"failToast": true},
		"comments off":    {"commentsOff": true},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSite()
			for k, v := range opt {
				s.post[k] = v
			}
			page, rec := s.open(t)
			if _, err := call(t, page, "comment_post", postURL, "hello"); err == nil {
				t.Fatalf("expected an error; posts: %v", apiPosts(rec))
			}
			if n := len(posts(rec, "*/comments/FXMEDIA1/add/")); n > 1 {
				t.Errorf("comment submitted %d times", n)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// like_comment
// ---------------------------------------------------------------------------

func TestLikeCommentByAuthor(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "like_comment", postURL, "fake.c1", "")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["liked_count"] != 1 || m["liked_authors"] != "fake.c1" {
		t.Errorf("result = %v", m)
	}
	if got := posts(rec, "*/comments/like/101/"); len(got) != 1 {
		t.Errorf("fake.c1's comment not liked exactly once: %v", apiPosts(rec))
	}
	if n := len(rec.Matching("POST", "*/comments/*like/*")); n != 1 {
		t.Errorf("other comments touched: %v", apiPosts(rec))
	}
	if n := len(posts(rec, "*/likes/*")); n != 0 {
		t.Errorf("the post itself was liked: %v", apiPosts(rec))
	}
}

func TestLikeCommentAuthorAlreadyLikedOrMissing(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "like_comment", postURL, "@fake.c2", 5)
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "already_liked" {
		t.Errorf("result = %v", m)
	}
	_, err = call(t, page, "like_comment", postURL, "fake.nobody", 5)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("missing author: err = %v", err)
	}
	if p := apiPosts(rec); len(p) != 0 {
		t.Errorf("nothing may be clicked: %v", p)
	}
}

func TestLikeCommentAuthorBehindLoadMore(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	if _, err := call(t, page, "like_comment", postURL, "fake.late", ""); err != nil {
		t.Fatal(err)
	}
	if got := posts(rec, "*/comments/like/105/"); len(got) != 1 {
		t.Errorf("fake.late's comment not liked: %v", apiPosts(rec))
	}
}

func TestLikeCommentsWithoutAuthorLikesUpToMax(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "like_comment", postURL, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, res)
	if m["liked_count"] != 2 || m["already_liked_count"] != 1 {
		t.Errorf("result = %v", m)
	}
	// 101 and 103 (102 is already liked and must not be toggled).
	for _, id := range []string{"101", "103"} {
		if len(posts(rec, "*/comments/like/"+id+"/")) != 1 {
			t.Errorf("comment %s not liked: %v", id, apiPosts(rec))
		}
	}
	if n := len(posts(rec, "*/comments/unlike/*")); n != 0 {
		t.Errorf("a comment was UNliked: %v", apiPosts(rec))
	}
}

func TestLikeCommentUnconfirmedAndNoComments(t *testing.T) {
	s := newSite()
	s.post["noconfirm"] = true
	page, _ := s.open(t)
	if _, err := call(t, page, "like_comment", postURL, "fake.c1", ""); err == nil {
		t.Fatal("unconfirmed comment like must fail")
	}
	s2 := newSite()
	s2.post["comments"] = []fx{}
	s2.post["hidden"] = []fx{}
	page2, rec2 := s2.open(t)
	if _, err := call(t, page2, "like_comment", postURL, "", 3); err == nil || !strings.Contains(err.Error(), "no comments") {
		t.Fatalf("no comments: err = %v", err)
	}
	if p := apiPosts(rec2); len(p) != 0 {
		t.Errorf("clicked something: %v", p)
	}
}

// ---------------------------------------------------------------------------
// reply_comment
// ---------------------------------------------------------------------------

func TestReplyToCommentByAuthor(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "reply_comment", postURL, "fake.c2", "thanks for the synthetic words")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["replied_to"] != "fake.c2" || m["status"] != "replied" {
		t.Errorf("result = %v", m)
	}
	got := posts(rec, "*/comments/FXMEDIA1/add/")
	if len(got) != 1 {
		t.Fatalf("add requests: %v", apiPosts(rec))
	}
	f := form(t, got[0])
	if f.Get("replied_to_comment_id") != "102" || f.Get("comment_text") != "@fake.c2 thanks for the synthetic words" {
		t.Errorf("reply posted as %v", f)
	}
}

func TestReplyToCommentFirstWhenNoAuthor(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	if _, err := call(t, page, "reply_comment", postURL, "", "hi c1"); err != nil {
		t.Fatal(err)
	}
	got := posts(rec, "*/comments/FXMEDIA1/add/")
	if len(got) != 1 || form(t, got[0]).Get("replied_to_comment_id") != "101" {
		t.Errorf("reply: %v", apiPosts(rec))
	}
}

func TestReplyToCommentRefusals(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	if _, err := call(t, page, "reply_comment", postURL, "fake.nobody", "hi"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("missing author: err = %v", err)
	}
	if p := apiPosts(rec); len(p) != 0 {
		t.Errorf("posted despite a missing author: %v", p)
	}
	s2 := newSite()
	s2.post["noconfirm"] = true
	page2, rec2 := s2.open(t)
	if _, err := call(t, page2, "reply_comment", postURL, "fake.c1", "hi"); err == nil {
		t.Fatalf("unconfirmed reply must fail; %v", apiPosts(rec2))
	}
}

// ---------------------------------------------------------------------------
// follow_user / unfollow_user
// ---------------------------------------------------------------------------

func TestFollowUserUsesOnlyTheHeaderButton(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "follow_user", "https://www.instagram.com/fake.ada/")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "followed" {
		t.Errorf("result = %v", m)
	}
	if n := len(posts(rec, "*/friendships/create/9001/")); n != 1 {
		t.Errorf("follow requests = %d: %v", n, apiPosts(rec))
	}
	if n := len(posts(rec, "*/friendships/create/777*")); n != 0 {
		t.Errorf("a suggested account was followed: %v", apiPosts(rec))
	}
}

func TestFollowUserStates(t *testing.T) {
	s := newSite()
	s.profiles["fake.following"] = fx{"state": "following", "message": true}
	s.profiles["fake.requested"] = fx{"state": "requested"}
	s.profiles["fake.private"] = fx{"state": "follow", "private": true}
	s.profiles["fake.back"] = fx{"state": "followback"}
	page, rec := s.open(t)
	for user, want := range map[string]string{
		"fake.following": "already_following",
		"fake.requested": "already_requested",
	} {
		res, err := call(t, page, "follow_user", user)
		if err != nil {
			t.Fatalf("%s: %v", user, err)
		}
		if m := resultMap(t, res); m["status"] != want {
			t.Errorf("%s: status = %v, want %s", user, m["status"], want)
		}
	}
	if p := apiPosts(rec); len(p) != 0 {
		t.Fatalf("already-followed profiles were clicked: %v", p)
	}
	res, err := call(t, page, "follow_user", "fake.private")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "requested" {
		t.Errorf("private: %v", m)
	}
	res, err = call(t, page, "follow_user", "fake.back")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "followed" {
		t.Errorf("follow back: %v", m)
	}
}

func TestFollowUserUnconfirmed(t *testing.T) {
	s := newSite()
	s.profiles["fake.stuck"] = fx{"state": "follow", "noconfirm": true}
	page, _ := s.open(t)
	if _, err := call(t, page, "follow_user", "fake.stuck"); err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnfollowUser(t *testing.T) {
	s := newSite()
	s.profiles["fake.following"] = fx{"state": "following", "message": true}
	s.profiles["fake.requested"] = fx{"state": "requested"}
	page, rec := s.open(t)
	for _, user := range []string{"fake.following", "fake.requested"} {
		res, err := call(t, page, "unfollow_user", "https://www.instagram.com/"+user+"/")
		if err != nil {
			t.Fatalf("%s: %v", user, err)
		}
		if m := resultMap(t, res); m["status"] != "unfollowed" {
			t.Errorf("%s: %v", user, m)
		}
	}
	if n := len(posts(rec, "*/friendships/destroy/9001/")); n != 2 {
		t.Errorf("destroy requests = %d: %v", n, apiPosts(rec))
	}
	rec.Reset()
	res, err := call(t, page, "unfollow_user", "fake.ada") // not followed
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "not_following" {
		t.Errorf("not followed: %v", m)
	}
	if p := apiPosts(rec); len(p) != 0 {
		t.Errorf("a not-followed profile was touched (would FOLLOW it): %v", p)
	}
}

func TestUnfollowUserUnconfirmed(t *testing.T) {
	s := newSite()
	s.profiles["fake.stuck"] = fx{"state": "following", "noconfirm": true}
	page, _ := s.open(t)
	if _, err := call(t, page, "unfollow_user", "fake.stuck"); err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// view_stories
// ---------------------------------------------------------------------------

func TestViewStoriesWatchesOnlyThisAccount(t *testing.T) {
	s := newSite()
	s.profiles["fake.story"] = fx{"state": "following", "story": true}
	page, rec := s.open(t)
	res, err := call(t, page, "view_stories", "https://www.instagram.com/fake.story/", 0)
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["stories_viewed"] != 3 {
		t.Errorf("result = %v", m)
	}
	for _, id := range []string{"3001", "3002", "3003"} {
		if len(posts(rec, "*/stories/reel/seen/?owner=fake.story&id="+id)) != 1 {
			t.Errorf("story %s not seen: %v", id, apiPosts(rec))
		}
	}
	if n := len(posts(rec, "*seen/?owner=fake.other*")); n > 1 {
		t.Errorf("kept watching the next account: %v", apiPosts(rec))
	}
	if n := len(rec.Matching("GET", "*/stories/highlights/*")); n != 0 {
		t.Errorf("a highlight was opened")
	}
}

func TestViewStoriesMaxAndNoStory(t *testing.T) {
	s := newSite()
	s.profiles["fake.story"] = fx{"state": "following", "story": true}
	page, rec := s.open(t)
	res, err := call(t, page, "view_stories", "fake.story", 2)
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["stories_viewed"] != 2 {
		t.Errorf("result = %v", m)
	}
	if n := len(posts(rec, "*seen/?owner=fake.story*")); n != 2 {
		t.Errorf("seen = %v", apiPosts(rec))
	}
	rec.Reset()
	res, err = call(t, page, "view_stories", "fake.ada", 0) // no active story, has a highlight
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["stories_viewed"] != 0 || m["status"] != "no_active_story" {
		t.Errorf("no story: %v", m)
	}
	if n := len(rec.Matching("GET", "*/stories/*")); n != 0 {
		t.Errorf("opened a story/highlight without an active story: %v", rec.Requests())
	}
}
