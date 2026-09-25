//go:build !nosocial

package tiktok

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// like_video
// ---------------------------------------------------------------------------

func TestLikeVideoLikesAndVerifies(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "like_video", vidPlain)
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "liked" || m["success"] != true {
		t.Fatalf("result = %v", m)
	}
	wantEvents(t, p, "video-like-click")
	if got := evalString(t, p, `document.getElementById('like-btn').getAttribute('aria-pressed')`); got != "true" {
		t.Fatalf("aria-pressed = %q after like", got)
	}
	// No marker left behind.
	if got := evalString(t, p, `String(document.querySelectorAll('[data-monoagent-tt]').length)`); got != "0" {
		t.Fatalf("%s markers left", got)
	}
}

// The pressed state lives on the button while the selector matches the icon
// span inside it: a liked video must not be clicked (that would unlike it).
func TestLikeVideoAlreadyLikedDoesNotClick(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "like_video", vidLiked)
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "already_liked" {
		t.Fatalf("result = %v", m)
	}
	wantEvents(t, p)
	if got := evalString(t, p, `document.getElementById('like-btn').getAttribute('aria-pressed')`); got != "true" {
		t.Fatalf("aria-pressed = %q, the video was unliked", got)
	}
}

func TestLikeVideoUnconfirmedIsError(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "like_video", vidLikeBroken)
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p, "video-like-click")
}

func TestLikeVideoUnknownStateRefusesToClick(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "like_video", vidNoPressed)
	if err == nil || !strings.Contains(err.Error(), "refusing to click") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

func TestLikeVideoNoSelectorMatchWithoutJevFails(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "like_video", vidAmbiguous)
	if err == nil || !strings.Contains(err.Error(), "like button not found") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

func TestLikeVideoJevPicksTheVideosLike(t *testing.T) {
	p := newPage(t)
	b, srv := jevBot(t, "Like video")
	res, err := call(t, b, p, "like_video", vidAmbiguous)
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "liked" {
		t.Fatalf("result = %v", m)
	}
	wantEvents(t, p, "video-like-click")
	in := jevIntents(srv)
	if len(in) != 1 || !strings.Contains(in[0], "not a comment's like button") || !strings.Contains(in[0], vidAmbiguous) {
		t.Fatalf("intents = %q", in)
	}
}

// Jev choosing a comment's like button is rejected, not clicked.
func TestLikeVideoJevPickOfCommentLikeIsRejected(t *testing.T) {
	p := newPage(t)
	b, _ := jevBot(t, "Like comment by Fake Ana")
	_, err := call(t, b, p, "like_video", vidAmbiguous)
	if err == nil || !strings.Contains(err.Error(), "belongs to a comment") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

func TestLikeVideoJevNoneFails(t *testing.T) {
	p := newPage(t)
	b, srv := jevBot(t, "")
	_, err := call(t, b, p, "like_video", vidAmbiguous)
	if err == nil || !strings.Contains(err.Error(), "jev fallback") {
		t.Fatalf("err = %v", err)
	}
	if srv.Calls() != 1 {
		t.Fatalf("jev calls = %d", srv.Calls())
	}
	wantEvents(t, p)
}

// A unique selector match never consults Jev.
func TestLikeVideoUniqueMatchSkipsJev(t *testing.T) {
	p := newPage(t)
	b, srv := jevBot(t, "Like video")
	if _, err := call(t, b, p, "like_video", vidPlain); err != nil {
		t.Fatal(err)
	}
	if srv.Calls() != 0 {
		t.Fatalf("jev calls = %d", srv.Calls())
	}
}

// ---------------------------------------------------------------------------
// like_comment
// ---------------------------------------------------------------------------

func TestLikeCommentByUserAndText(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "like_comment", vidPlain, "", "@fake_ana", "Love the colors in this one")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "liked" {
		t.Fatalf("result = %v", m)
	}
	wantEvents(t, p, "comment-like-click:fake_ana")
}

func TestLikeCommentAlreadyLikedDoesNotClick(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "like_comment", vidPlain, "", "fake_ben", "First!")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "already_liked" {
		t.Fatalf("result = %v", m)
	}
	wantEvents(t, p)
}

// "Love the colors" matches Ana's and Cy's comments: without the username
// that is ambiguous and nothing is clicked; with it only Cy's is liked.
func TestLikeCommentAmbiguousTextFails(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "like_comment", vidPlain, "", "", "Love the colors")
	if err == nil || !strings.Contains(err.Error(), "2 comments match") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
	if _, err := call(t, &TikTokBot{}, p, "like_comment", vidPlain, "", "fake_cy", "Love the colors"); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, p, "comment-like-click:fake_cy")
}

func TestLikeCommentWrongUserIsNotFound(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "like_comment", vidPlain, "", "fake_ben", "Love the colors in this one")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

func TestLikeCommentScrollsToFindIt(t *testing.T) {
	p := newPage(t)
	if _, err := call(t, &TikTokBot{}, p, "like_comment", vidPlain, "", "fake_eve", "Only visible after scrolling"); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, p, "comment-like-click:fake_eve")
}

// A comment id TikTok does not render cannot identify a comment; the old
// data-comment-id lookup would silently fail — now it is an explicit error.
func TestLikeCommentUnknownIDFails(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "like_comment", vidPlain, "7123", "", "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

func TestLikeCommentNeedsTextOrID(t *testing.T) {
	p := newPage(t)
	if _, err := call(t, &TikTokBot{}, p, "like_comment", vidPlain, "", "fake_ana", ""); err == nil || !strings.Contains(err.Error(), "needs the comment's text") {
		t.Fatalf("err = %v", err)
	}
}

// The comment has no like button matching the selectors: Jev picks one,
// which must lie inside the matched comment.
func TestLikeCommentJevPickMustBeInsideTheComment(t *testing.T) {
	p := newPage(t)
	b, _ := jevBot(t, "Like video")
	_, err := call(t, b, p, "like_comment", vidLikeless, "", "fake_ana", "Love the colors in this one")
	if err == nil || !strings.Contains(err.Error(), "not inside the target comment") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

func TestLikeCommentWithoutButtonAndJevFails(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "like_comment", vidLikeless, "", "fake_ana", "Love the colors")
	if err == nil || !strings.Contains(err.Error(), "like button of comment by @fake_ana") {
		t.Fatalf("err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// follow_user
// ---------------------------------------------------------------------------

func TestFollowUserFollowsAndVerifies(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "follow_user", profileURL("fake_creator"))
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "followed" || m["username"] != "fake_creator" {
		t.Fatalf("result = %v", m)
	}
	// The suggested account's Follow button was never touched.
	wantEvents(t, p, "follow-click:fake_creator")
}

func TestFollowUserAlreadyFollowingDoesNotClick(t *testing.T) {
	for _, h := range []string{"fake_following", "fake_friends"} {
		t.Run(h, func(t *testing.T) {
			p := newPage(t)
			res, err := call(t, &TikTokBot{}, p, "follow_user", "@"+h)
			if err != nil {
				t.Fatal(err)
			}
			if m := resultMap(t, res); m["status"] != "already_following" {
				t.Fatalf("result = %v", m)
			}
			wantEvents(t, p)
		})
	}
}

func TestFollowUserPrivateBecomesRequested(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "follow_user", profileURL("fake_private"))
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "requested" {
		t.Fatalf("result = %v", m)
	}
}

func TestFollowUserUnconfirmedIsError(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "follow_user", profileURL("fake_stuck"))
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p, "follow-click:fake_stuck")
}

func TestFollowUserUnreadableStateRefusesToClick(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "follow_user", profileURL("fake_foreign"))
	if err == nil || !strings.Contains(err.Error(), "refusing to click") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

// ---------------------------------------------------------------------------
// comment_on_video
// ---------------------------------------------------------------------------

func TestCommentOnVideoPostsAndVerifies(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "comment_on_video", vidPlain, "Nice work, synthetic tester!")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "commented" || m["verified"] != "comment_rendered" {
		t.Fatalf("result = %v", m)
	}
	wantEvents(t, p, "comment-post:Nice work, synthetic tester!")
}

func TestCommentOnVideoFailureIsError(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "comment_on_video", vidCommentBad, "This will not post")
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p, "comment-post:This will not post")
}

func TestCommentOnVideoCommentsOffIsError(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "comment_on_video", vidNoComments, "hello")
	if err == nil || !strings.Contains(err.Error(), "comment button not found") {
		t.Fatalf("err = %v", err)
	}
}

// Two contenteditables and no data-e2e: without Jev nothing is typed; with
// Jev the comment box (not the message box) and the comment's Post button
// are picked and the comment is verified.
func TestCommentOnVideoAmbiguousBoxes(t *testing.T) {
	p := newPage(t)
	if _, err := call(t, &TikTokBot{}, p, "comment_on_video", vidAmbiguous, "hi"); err == nil || !strings.Contains(err.Error(), "comment box not found") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)

	srvWant := map[string]string{"fill": "Add comment", "click": "Post comment"}
	b, srv := jevBot(t, "")
	srv.SetHandler(func(req jevReq) map[string]string {
		in := intentOf(req)
		want := srvWant["click"]
		if strings.Contains(in, "text box") {
			want = srvWant["fill"]
		}
		return pickLabel(want)(req)
	})
	res, err := call(t, b, p, "comment_on_video", vidAmbiguous, "Picked by Jev")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "commented" {
		t.Fatalf("result = %v", m)
	}
	wantEvents(t, p, "comment-post:Picked by Jev")
	if got := evalString(t, p, `document.getElementById('dm-box').innerText`); got != "" {
		t.Fatalf("message box got text %q", got)
	}
	if srv.Calls() != 2 {
		t.Fatalf("jev calls = %d, want 2 (box, post button)", srv.Calls())
	}
}

// ---------------------------------------------------------------------------
// share / stitch / duet
// ---------------------------------------------------------------------------

func TestShareStitchDuet(t *testing.T) {
	for _, c := range []struct{ method, opened, status string }{
		{"share_video", "copy", "link_copied"},
		{"stitch_video", "stitch", "opened_stitch_editor"},
		{"duet_video", "duet", "opened_duet_editor"},
	} {
		t.Run(c.method, func(t *testing.T) {
			p := newPage(t)
			res, err := call(t, &TikTokBot{}, p, c.method, vidPlain)
			if err != nil {
				t.Fatal(err)
			}
			m := resultMap(t, res)
			if m["status"] != c.status || m["published"] != false {
				t.Fatalf("result = %v", m)
			}
			if got := evalString(t, p, `document.body.getAttribute('data-opened') || ''`); got != c.opened {
				t.Fatalf("opened %q, want %q", got, c.opened)
			}
		})
	}
}

func TestStitchDuetDisabledIsError(t *testing.T) {
	for _, m := range []string{"stitch_video", "duet_video"} {
		t.Run(m, func(t *testing.T) {
			p := newPage(t)
			_, err := call(t, &TikTokBot{}, p, m, vidRemixOff)
			if err == nil || !strings.Contains(err.Error(), "disabled") {
				t.Fatalf("err = %v", err)
			}
			if got := evalString(t, p, `document.body.getAttribute('data-opened') || ''`); got != "" {
				t.Fatalf("opened %q", got)
			}
		})
	}
}
