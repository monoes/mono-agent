//go:build social

package instagram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendMessageViaProfileButton(t *testing.T) {
	s := newSite()
	s.profiles["fake.bea"] = fx{"state": "following", "message": true, "thread": "5001"}
	page, rec := s.open(t)
	msg := "Hello from a synthetic test 👋"
	res, err := call(t, page, "send_message", "https://www.instagram.com/fake.bea/", msg)
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["method"] != "profile" || m["username"] != "fake.bea" {
		t.Errorf("result = %v", m)
	}
	got := posts(rec, "*/direct_v2/threads/broadcast/text/")
	if len(got) != 1 {
		t.Fatalf("sends = %v", apiPosts(rec))
	}
	f := form(t, got[0])
	if f.Get("thread") != "/direct/t/5001/" || f.Get("text") != msg {
		t.Errorf("sent %v", f)
	}
	if n := len(posts(rec, "*/friendships/*")); n != 0 {
		t.Errorf("follow state touched while messaging: %v", apiPosts(rec))
	}
}

func TestSendMessageComposeFlowPicksExactUsername(t *testing.T) {
	s := newSite()
	s.profiles["fake.bea"] = fx{"state": "follow", "message": false}
	page, rec := s.open(t)
	res, err := call(t, page, "send_message", "fake.bea", "hi via compose")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["method"] != "compose" {
		t.Errorf("result = %v", m)
	}
	sel := posts(rec, "*/direct_v2/select/*")
	if len(sel) != 1 || !strings.HasSuffix(sel[0].URL, "u=fake.bea") {
		t.Fatalf("selected %v (fake.bea.two is listed first and must not be chosen)", apiPosts(rec))
	}
	got := posts(rec, "*/broadcast/text/")
	if len(got) != 1 || form(t, got[0]).Get("thread") != "/direct/t/5001/" {
		t.Errorf("sends = %v", apiPosts(rec))
	}
}

func TestSendMessageWithEnterWhenNoSendButton(t *testing.T) {
	s := newSite()
	s.profiles["fake.bea"] = fx{"state": "following", "message": true}
	s.thread["noSendButton"] = true
	page, rec := s.open(t)
	if _, err := call(t, page, "send_message", "fake.bea", "via Enter"); err != nil {
		t.Fatal(err)
	}
	if n := len(posts(rec, "*/broadcast/text/")); n != 1 {
		t.Errorf("sends = %v", apiPosts(rec))
	}
}

func TestSendMessageUsesSendButtonWhenEnterDoesNothing(t *testing.T) {
	s := newSite()
	s.profiles["fake.bea"] = fx{"state": "following", "message": true}
	s.thread["enterDoesNothing"] = true
	page, rec := s.open(t)
	if _, err := call(t, page, "send_message", "fake.bea", "via the Send button"); err != nil {
		t.Fatal(err)
	}
	if n := len(posts(rec, "*/broadcast/text/")); n != 1 {
		t.Errorf("sends = %v", apiPosts(rec))
	}
}

func TestSendMessageUnconfirmedIsNotRetried(t *testing.T) {
	for name, opt := range map[string]fx{"no confirmation": {"noconfirm": true}, "not delivered": {"fail": true}} {
		t.Run(name, func(t *testing.T) {
			s := newSite()
			s.profiles["fake.bea"] = fx{"state": "following", "message": true}
			s.thread = opt
			page, rec := s.open(t)
			if _, err := call(t, page, "send_message", "fake.bea", "will it arrive"); err == nil {
				t.Fatal("an unverified send must fail")
			}
			// One attempt only: no second route after typing (no double send).
			if n := len(posts(rec, "*/broadcast/text/")); n != 1 {
				t.Errorf("sends = %v", apiPosts(rec))
			}
			if n := len(rec.Matching("GET", "https://www.instagram.com/direct/new/")); n != 0 {
				t.Errorf("fell back to the compose flow after typing")
			}
		})
	}
}

func TestSendMessageUnconfirmedAndNoRecipient(t *testing.T) {
	s := newSite()
	s.profiles["fake.nobody"] = fx{"state": "follow", "message": false}
	page, rec := s.open(t)
	_, err := call(t, page, "send_message", "fake.nobody", "hello?")
	if err == nil || !strings.Contains(err.Error(), "could not open a conversation") {
		t.Fatalf("err = %v", err)
	}
	if n := len(posts(rec, "*/broadcast/*")); n != 0 {
		t.Errorf("sent to someone: %v", apiPosts(rec))
	}
	if n := len(posts(rec, "*/direct_v2/select/*")); n != 0 {
		t.Errorf("selected a near-match recipient: %v", apiPosts(rec))
	}
}

func TestReplyToConversationByURL(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "reply_to_conversation", "https://www.instagram.com/direct/t/5002/", "synthetic reply")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "sent" {
		t.Errorf("result = %v", m)
	}
	got := posts(rec, "*/broadcast/text/")
	if len(got) != 1 || form(t, got[0]).Get("thread") != "/direct/t/5002/" {
		t.Errorf("sends = %v", apiPosts(rec))
	}
}

func TestReplyToConversationByUsernameInInbox(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	res, err := call(t, page, "reply_to_conversation", "fake.bea", "inbox reply")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["method"] != "inbox" {
		t.Errorf("result = %v", m)
	}
	if n := len(posts(rec, "*/notifications/dismiss/")); n != 1 {
		t.Errorf("the notifications dialog was not dismissed: %v", apiPosts(rec))
	}
	got := posts(rec, "*/broadcast/text/")
	// fake.bea.two is also in the inbox; only the exact username counts.
	if len(got) != 1 || form(t, got[0]).Get("thread") != "/direct/t/5001/" {
		t.Errorf("sends = %v", apiPosts(rec))
	}
}

func TestReplyToConversationNotInInboxStartsIt(t *testing.T) {
	s := newSite()
	s.profiles["fake.zed"] = fx{"state": "following", "message": true, "thread": "5002"}
	page, rec := s.open(t)
	res, err := call(t, page, "reply_to_conversation", "fake.zed", "starting fresh")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["method"] != "new_message" {
		t.Errorf("result = %v", m)
	}
	if n := len(posts(rec, "*/broadcast/text/")); n != 1 {
		t.Errorf("sends = %v", apiPosts(rec))
	}
}

// ---------------------------------------------------------------------------
// publish_content
// ---------------------------------------------------------------------------

func mediaFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "synthetic.png")
	// A 1×1 PNG.
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0a, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
	if err := os.WriteFile(p, png, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublishContent(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	media := mediaFile(t)
	caption := "A synthetic caption ☀️ #fixture"
	res, err := call(t, page, "publish_content", media, caption, "Fake Plaza")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["success"] != true {
		t.Errorf("result = %v", m)
	}
	got := posts(rec, "*/api/v1/media/configure/")
	if len(got) != 1 {
		t.Fatalf("configure requests: %v", apiPosts(rec))
	}
	f := form(t, got[0])
	if f.Get("caption") != caption || f.Get("location") != "Fake Plaza, Testville" || f.Get("file") != "synthetic.png" {
		t.Errorf("published %v", f)
	}
}

func TestPublishContentRefusals(t *testing.T) {
	s := newSite()
	page, rec := s.open(t)
	media := mediaFile(t)
	if _, err := call(t, page, "publish_content", media, "caption", "Nowhere Land"); err == nil || !strings.Contains(err.Error(), "location") {
		t.Fatalf("unmatched location: err = %v", err)
	}
	if n := len(posts(rec, "*/media/configure/")); n != 0 {
		t.Fatalf("published without the requested location: %v", apiPosts(rec))
	}
	if _, err := call(t, page, "publish_content", "https://example.test/x.jpg", "caption"); err == nil {
		t.Fatal("a URL is not a local media file")
	}
	if _, err := call(t, page, "publish_content", filepath.Join(t.TempDir(), "missing.png"), "caption"); err == nil {
		t.Fatal("a missing file must fail before touching the page")
	}
	s2 := newSite()
	s2.home["noconfirm"] = true
	page2, _ := s2.open(t)
	if _, err := call(t, page2, "publish_content", media, "caption"); err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("unconfirmed share: err = %v", err)
	}
	s3 := newSite()
	s3.home["fail"] = true
	page3, _ := s3.open(t)
	if _, err := call(t, page3, "publish_content", media, "caption"); err == nil || !strings.Contains(err.Error(), "couldn") {
		t.Fatalf("failed share: err = %v", err)
	}
}
