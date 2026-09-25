//go:build social

package linkedin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

var shareRoute = bottest.Route{Pattern: "https://www.linkedin.com/feed/*", File: "testdata/feed_share.html"}

func TestPublishPost(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	const text = "Launching our offline-first notes app today.\nFeedback welcome!"

	t.Run("text post", func(t *testing.T) {
		p, rec := newPage(t, b, shareRoute)
		res, err := call(t, &LinkedInBot{}, p, "publish_post", text, "")
		if err != nil {
			t.Fatal(err)
		}
		if m := res.(map[string]interface{}); m["post_url"] != "https://www.linkedin.com/feed/update/urn:li:activity:7400000000000000001/" {
			t.Fatalf("res = %v", m)
		}
		w := writes(rec)
		if len(w) != 1 || !strings.HasPrefix(w[0], "shares/create ") || !strings.Contains(w[0], "Launching our offline-first notes app today.") {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("with media", func(t *testing.T) {
		dir := t.TempDir()
		img := filepath.Join(dir, "photo.png")
		if err := os.WriteFile(img, []byte("\x89PNG fake"), 0o600); err != nil {
			t.Fatal(err)
		}
		p, rec := newPage(t, b, shareRoute)
		if _, err := call(t, &LinkedInBot{}, p, "publish_post", "Photo day", img); err != nil {
			t.Fatal(err)
		}
		w := writes(rec)
		if len(w) != 1 || !strings.Contains(w[0], `"media":["photo.png"]`) {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("post that never goes out is an error", func(t *testing.T) {
		src, err := os.ReadFile("testdata/feed_share.html")
		if err != nil {
			t.Fatal(err)
		}
		broken := strings.Replace(string(src), "new URLSearchParams(location.search)", "new URLSearchParams('broken=post')", 1)
		p, rec := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/feed/*", Body: broken})
		_, err = call(t, &LinkedInBot{}, p, "publish_post", text, "")
		if err == nil || !strings.Contains(err.Error(), "not confirmed") {
			t.Fatalf("err = %v", err)
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("writes = %v", w)
		}
	})

	t.Run("empty text", func(t *testing.T) {
		p, rec := newPage(t, b, shareRoute)
		if _, err := call(t, &LinkedInBot{}, p, "publish_post", "  ", ""); err == nil {
			t.Fatal("want error")
		}
		if len(rec.Requests()) != 0 {
			t.Fatal("navigated for an empty post")
		}
	})
}
