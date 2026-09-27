package personitem

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot"
	_ "github.com/monoes/mono-agent/internal/bot/hackernews"
	_ "github.com/monoes/mono-agent/internal/bot/instagram"
	_ "github.com/monoes/mono-agent/internal/bot/linkedin"
	_ "github.com/monoes/mono-agent/internal/bot/tiktok"
	_ "github.com/monoes/mono-agent/internal/bot/x"
)

func TestResolve(t *testing.T) {
	_, social := bot.PlatformRegistry["LINKEDIN"]
	cases := []struct {
		name       string
		platform   string
		item       map[string]interface{}
		want       string
		wantAuthor bool
		wantName   string
		socialOnly bool
	}{
		{name: "linkedin profile", platform: "linkedin",
			item: map[string]interface{}{"url": "https://www.linkedin.com/in/ada-first-test/"}, want: "ada-first-test"},
		// The bug: a post permalink's last segment is the activity URN.
		{name: "linkedin post without author", platform: "LINKEDIN",
			item: map[string]interface{}{"url": "https://www.linkedin.com/feed/update/urn:li:activity:7509522219027554305/"}},
		{name: "linkedin post links its author", platform: "linkedin",
			item: map[string]interface{}{
				"url":        "https://www.linkedin.com/feed/update/urn:li:activity:7509522219027554305/",
				"author":     "Ada First",
				"author_url": "https://www.linkedin.com/in/ada-first-test/",
			}, want: "ada-first-test", wantAuthor: true, wantName: "Ada First"},
		{name: "linkedin company author is nobody", platform: "linkedin",
			item: map[string]interface{}{
				"url":        "https://www.linkedin.com/feed/update/urn:li:activity:1/",
				"author":     "Example Works",
				"author_url": "https://www.linkedin.com/company/example-works/",
			}},
		{name: "instagram post links its author, whose name is a handle", platform: "instagram",
			item: map[string]interface{}{
				"url": "https://www.instagram.com/p/CD61bhxKOQh/", "author": "natgeo", "author_url": "https://www.instagram.com/natgeo/",
			}, want: "natgeo", wantAuthor: true, socialOnly: true},
		{name: "instagram post without author", platform: "instagram",
			item: map[string]interface{}{"url": "https://www.instagram.com/p/CD61bhxKOQh/"}},
		{name: "tiktok video without handle", platform: "tiktok",
			item: map[string]interface{}{"url": "https://www.tiktok.com/video/7400000000000000000"}},
		{name: "hacker news item", platform: "hackernews",
			item: map[string]interface{}{"url": "https://news.ycombinator.com/item?id=1"}},
		{name: "profile_url wins over url", platform: "linkedin",
			item: map[string]interface{}{"profile_url": "https://www.linkedin.com/in/pat/", "url": "https://www.linkedin.com/feed/update/urn:li:activity:2/"}, want: "pat"},
		// Platforms without an adapter keep the last-segment fallback, but
		// never for post/status URLs or numeric ids.
		{name: "generic profile", platform: "mastodon",
			item: map[string]interface{}{"url": "https://example.social/@kit"}, want: "kit"},
		{name: "generic status", platform: "mastodon",
			item: map[string]interface{}{"url": "https://example.social/@kit/status/12345"}},
		{name: "generic numeric id", platform: "mastodon",
			item: map[string]interface{}{"url": "https://example.social/@kit/112233"}},
		{name: "generic urn", platform: "mastodon",
			item: map[string]interface{}{"url": "https://example.social/x/urn:li:activity:1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.socialOnly && !social {
				t.Skip("nosocial build")
			}
			ref, ok := Resolve(c.platform, c.item)
			if c.want == "" {
				if ok {
					t.Fatalf("resolved to %+v, want nobody", ref)
				}
				return
			}
			if !ok || ref.Username != c.want || ref.Author != c.wantAuthor || ref.FullName != c.wantName {
				t.Fatalf("got %+v ok=%v, want username %q author=%v name %q", ref, ok, c.want, c.wantAuthor, c.wantName)
			}
		})
	}
}

func TestProfileOf(t *testing.T) {
	p := ProfileOf(map[string]interface{}{
		"full_name":           "Ada First",
		"headline":            "Robotics engineer",
		"profile_picture_url": "https://media.example/ada.jpg",
		"about":               "Builds things.",
		"location":            "Lisbon",
		"experience":          []interface{}{map[string]interface{}{"title": "Engineer", "company": "Example Works"}},
		"education":           []interface{}{},
	})
	if p.FullName != "Ada First" || p.ImageURL != "https://media.example/ada.jpg" || p.About != "Builds things." ||
		p.JobTitle != "Robotics engineer" || p.Location != "Lisbon" {
		t.Fatalf("profile = %+v", p)
	}
	if p.Experience != `[{"company":"Example Works","title":"Engineer"}]` {
		t.Fatalf("experience = %s", p.Experience)
	}
	if p.Education != "" {
		t.Fatalf("empty education = %q, want none", p.Education)
	}
	if got := ProfileOf(map[string]interface{}{"job_title": "CTO", "headline": "Builder"}).JobTitle; got != "CTO" {
		t.Fatalf("job_title = %q", got)
	}
	if got := JSONList(`[{"school":"X"}]`); got != `[{"school":"X"}]` {
		t.Fatalf("JSONList(string) = %q", got)
	}
	if got := JSONList("not json"); got != "" {
		t.Fatalf("JSONList(garbage) = %q", got)
	}
}

// A profile read's bio is the person's About, on every platform: it never
// becomes the introduction (the drafted outreach message), and the
// platform's account category never becomes the review category.
func TestProfileOfBioIsAboutNotIntroduction(t *testing.T) {
	for _, item := range []map[string]interface{}{
		{"platform": "INSTAGRAM", "bio": "Synthetic IG bio", "profile_category": "Artist"},
		{"platform": "TIKTOK", "bio": "Synthetic IG bio"},
		{"platform": "X", "bio": "Synthetic IG bio"},
		{"platform": "LINKEDIN", "about": "Synthetic IG bio"},
		{"biography": "Synthetic IG bio"},
	} {
		p := ProfileOf(item)
		if p.About != "Synthetic IG bio" {
			t.Errorf("%v: about = %q", item, p.About)
		}
		if strings.Contains(p.Details, "Synthetic IG bio") {
			t.Errorf("%v: bio leaked into details %s", item, p.Details)
		}
	}
}

func TestProfileOfDetails(t *testing.T) {
	p := ProfileOf(map[string]interface{}{
		"full_name":        "Ada Fixture",
		"bio":              "Synthetic bio",
		"links":            []interface{}{map[string]interface{}{"url": "https://example.test/a", "title": "Shop"}, map[string]interface{}{"url": ""}},
		"pronouns":         []string{"she/her"},
		"profile_category": "Artist",
		"is_private":       false,
		"is_protected":     true, // is_private came first
		"contact":          map[string]interface{}{"email": "ada@example.test", "phone": ""},
		"highlights":       []interface{}{},
		"likes_count":      "5.6K",
		"friend_count":     float64(17),
		"cover_image_url":  "https://media.example/cover.jpg",
		"threads_handle":   "  ",
		"pinned_post":      map[string]interface{}{"url": "https://x.com/fake/status/1", "text": "Hello"},
	})
	var d map[string]interface{}
	if err := json.Unmarshal([]byte(p.Details), &d); err != nil {
		t.Fatalf("details %q: %v", p.Details, err)
	}
	want := map[string]interface{}{
		"links":            []interface{}{map[string]interface{}{"url": "https://example.test/a", "title": "Shop"}},
		"pronouns":         []interface{}{"she/her"},
		"profile_category": "Artist",
		"is_private":       false,
		"contact":          map[string]interface{}{"email": "ada@example.test"},
		"likes_count":      float64(5600),
		"friend_count":     float64(17),
		"banner_url":       "https://media.example/cover.jpg",
		"pinned_post":      map[string]interface{}{"url": "https://x.com/fake/status/1", "text": "Hello"},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("details = %v\nwant      %v", d, want)
	}
	if got := ProfileOf(map[string]interface{}{"full_name": "Nobody"}).Details; got != "" {
		t.Fatalf("no details = %q, want none", got)
	}
}
