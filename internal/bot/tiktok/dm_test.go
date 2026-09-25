//go:build !nosocial

package tiktok

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

func TestSendDMSendsAndVerifies(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "send_dm", profileURL("fake_creator"), "Hello from a synthetic test")
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, res)
	if m["status"] != "sent" || m["verified"] != "message_rendered" || m["username"] != "fake_creator" {
		t.Fatalf("result = %v", m)
	}
	wantEvents(t, p, "send:fake_creator:Hello from a synthetic test")
}

func TestSendDMAcceptsHandleAndBotAdapterSendMessage(t *testing.T) {
	p := newPage(t)
	if err := (&TikTokBot{}).SendMessage(bg, p, "fake_creator", "via SendMessage"); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, p, "send:fake_creator:via SendMessage")
}

// The Message button led to somebody else's chat: nothing is typed or sent.
func TestSendDMWrongRecipientIsRefused(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "send_dm", profileURL("fake_wrongchat"), "not for you")
	if err == nil || !strings.Contains(err.Error(), "not @fake_wrongchat") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
	if got := evalString(t, p, `document.getElementById('composer').innerText`); got != "" {
		t.Fatalf("composer holds %q", got)
	}
}

func TestSendDMUnverifiedIsError(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "send_dm", profileURL("fake_mute"), "lost message")
	if err == nil || !strings.Contains(err.Error(), "could not be verified") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p, "send:fake_mute:lost message")
}

// No Message button: fail, and never click Follow (or anything) instead.
func TestSendDMWithoutMessageButtonFails(t *testing.T) {
	p := newPage(t)
	_, err := call(t, &TikTokBot{}, p, "send_dm", profileURL("fake_nodm"), "hi")
	if err == nil || !strings.Contains(err.Error(), "Message button not found") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

// Jev picking the Follow button for "Message" is rejected.
func TestSendDMJevPickOfFollowIsRejected(t *testing.T) {
	p := newPage(t)
	b, _ := jevBot(t, "Follow")
	_, err := call(t, b, p, "send_dm", profileURL("fake_nodm"), "hi")
	if err == nil || !strings.Contains(err.Error(), "follow control") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

func TestReplyToConversation(t *testing.T) {
	p := newPage(t)
	res, err := call(t, &TikTokBot{}, p, "reply_to_conversation", "Fake Ana", "Thanks for writing!")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "sent" || m["conversation"] != "Fake Ana" || m["recipient"] != "fake_ana" {
		t.Fatalf("result = %v", m)
	}
	wantEvents(t, p, "send:fake_ana:Thanks for writing!")
}

func TestReplyToConversationAmbiguousOrMissing(t *testing.T) {
	p := newPage(t)
	if _, err := call(t, &TikTokBot{}, p, "reply_to_conversation", "Fake Cy", "x"); err == nil || !strings.Contains(err.Error(), "2 conversations") {
		t.Fatalf("err = %v", err)
	}
	if _, err := call(t, &TikTokBot{}, p, "reply_to_conversation", "Nobody Here", "x"); err == nil || !strings.Contains(err.Error(), "no conversation") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p)
}

// attachUpload plays the action's upload step: set a file on the input.
func attachUpload(t *testing.T, p *bottest.Page, url string) {
	t.Helper()
	if err := p.Navigate(url); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "holiday_clip.mp4")
	if err := os.WriteFile(f, []byte("not really a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := p.Element("input[type='file']", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := in.SetFiles([]string{f}); err != nil {
		t.Fatal(err)
	}
}

func TestPublishUploadedVideoReplacesCaptionAndVerifies(t *testing.T) {
	p := newPage(t)
	attachUpload(t, p, "https://www.tiktok.com/tiktokstudio/upload?from=webapp")
	res, err := call(t, &TikTokBot{}, p, "publish_uploaded_video", "Synthetic caption #test")
	if err != nil {
		t.Fatal(err)
	}
	if m := resultMap(t, res); m["status"] != "published" || m["verified"] != "redirected_to_content" {
		t.Fatalf("result = %v", m)
	}
	// The caption TikTok prefilled from the file name was replaced.
	if got := evalString(t, p, `document.body.getAttribute('data-posted')`); got != "posted:Synthetic caption #test" {
		t.Fatalf("posted %q", got)
	}
}

func TestPublishUploadedVideoUnconfirmedIsError(t *testing.T) {
	p := newPage(t)
	attachUpload(t, p, "https://www.tiktok.com/tiktokstudio/upload?fail=1")
	_, err := call(t, &TikTokBot{}, p, "publish_uploaded_video", "never lands")
	if err == nil || !strings.Contains(err.Error(), "publish not confirmed") {
		t.Fatalf("err = %v", err)
	}
	wantEvents(t, p, "file:holiday_clip.mp4", "post:never lands")
}

func TestPublishWithoutUploadIsError(t *testing.T) {
	p := newPage(t)
	if err := p.Navigate("https://www.tiktok.com/tiktokstudio/upload"); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, &TikTokBot{}, p, "publish_uploaded_video", "x"); err == nil || !strings.Contains(err.Error(), "caption box not found") {
		t.Fatalf("err = %v", err)
	}
}
