//go:build !nosocial

package instagram

// A fake instagram.com for the browser tests: every request is answered
// from the synthetic fixtures in testdata/ (bottest never touches the
// network), every write the page makes is a POST to /api/... that the
// Recorder captures, and the page carries an Instagram-like CSP without
// 'unsafe-eval' so only CSP-proof evaluation works.
//
//	BOTTEST_BROWSER=/usr/bin/chromium go test ./internal/bot/instagram/

import (
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

func TestMain(m *testing.M) {
	pageSettle = 150 * time.Millisecond
	uiPause = 100 * time.Millisecond
	findTimeout = 4 * time.Second
	verifyTimeout = 3 * time.Second
	pollEvery = 100 * time.Millisecond
	storyDwell = 150 * time.Millisecond
	scrollSettle = 300 * time.Millisecond
	publishTimeout = 4 * time.Second
	sendVerificationTimeout = 3 * time.Second
	os.Exit(m.Run())
}

type fx map[string]interface{}

// site configures the fake instagram.com.
type site struct {
	profiles   map[string]fx // username → profile.html FX (default: not followed, Message button)
	post       fx            // post.html FX
	postPage   string        // post fixture (default post.html)
	postAuthor string
	stories    fx
	inbox      fx
	threads    map[string]string // thread id → peer username
	thread     fx                // thread.html FX
	newMsg     fx
	search     fx
	home       fx
	profileAPI bool // serve web_profile_info (else 404)
}

func defaultComments() []fx {
	return []fx{
		{"id": "101", "author": "fake.c1", "text": "First synthetic comment", "likes": 2},
		{"id": "102", "author": "fake.c2", "text": "Second synthetic comment", "liked": true, "replies": []fx{
			{"id": "103", "author": "fake.c3", "text": "A synthetic reply to c2"},
		}},
		{"id": "104", "author": "fake.author", "text": "Thanks everyone"},
	}
}

func newSite() *site {
	return &site{
		profiles:   map[string]fx{},
		post:       fx{"comments": defaultComments(), "hidden": []fx{{"id": "105", "author": "fake.late", "text": "A comment behind load more"}}},
		postAuthor: "fake.author",
		stories:    fx{},
		inbox:      fx{},
		threads:    map[string]string{"5001": "fake.bea", "5002": "fake.cy", "5003": "fake.bea.two", "5009": "fake.beatrix"},
		newMsg:     fx{},
		thread:     fx{},
		search:     fx{},
		home:       fx{},
	}
}

func render(t *testing.T, file string, f fx, vars map[string]string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if f == nil {
		f = fx{}
	}
	j, _ := json.Marshal(f)
	s := strings.ReplaceAll(string(b), "{{FX}}", string(j))
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

var (
	postPathRe  = regexp.MustCompile(`^/(?:([A-Za-z0-9._]+)/)?(p|reel|tv)/([A-Za-z0-9_-]+)/`)
	threadRe    = regexp.MustCompile(`^/direct/t/([^/]+)/`)
	profilePath = regexp.MustCompile(`^/([A-Za-z0-9._]+)/`)
)

func html(body string) bottest.Response {
	return bottest.Response{Body: body, ContentType: "text/html; charset=utf-8"}
}

// open starts a browser page serving the site.
func (s *site) open(t *testing.T) (*bottest.Page, *bottest.Recorder) {
	t.Helper()
	b := bottest.Launch(t)
	page := b.NewPage(t)
	page.SetCSP("script-src 'self' 'unsafe-inline'")
	rec := page.Serve(
		bottest.Route{Pattern: "https://scontent.fixture.invalid/*", Body: "", ContentType: "image/gif"},
		bottest.Route{Pattern: "https://www.instagram.com/api/v1/users/web_profile_info/*", Handler: func(r bottest.Request) bottest.Response {
			if !s.profileAPI {
				return bottest.Response{Status: 404, Body: `{"message":"not found","status":"fail"}`, ContentType: "application/json"}
			}
			u, _ := url.Parse(r.URL)
			return bottest.Response{Body: render(t, "web_profile_info.json", nil, map[string]string{"USER": u.Query().Get("username")}), ContentType: "application/json"}
		}},
		bottest.Route{Pattern: "https://www.instagram.com/api/*", Method: "POST", Body: `{"status":"ok"}`, ContentType: "application/json"},
		bottest.Route{Pattern: "https://www.instagram.com/*", Handler: func(r bottest.Request) bottest.Response {
			u, _ := url.Parse(r.URL)
			p := u.Path
			switch {
			case p == "/" || p == "":
				return html(render(t, "home.html", s.home, nil))
			case strings.HasPrefix(p, "/stories/"):
				return html(render(t, "stories.html", s.stories, nil))
			case strings.HasPrefix(p, "/direct/inbox"):
				return html(render(t, "inbox.html", s.inbox, nil))
			case strings.HasPrefix(p, "/direct/new"):
				return html(render(t, "new.html", s.newMsg, nil))
			case threadRe.MatchString(p):
				peer := s.threads[threadRe.FindStringSubmatch(p)[1]]
				return html(render(t, "thread.html", s.thread, map[string]string{"PEER": peer}))
			case strings.HasPrefix(p, "/explore/"):
				return html(render(t, "search.html", s.search, nil))
			case postPathRe.MatchString(p):
				m := postPathRe.FindStringSubmatch(p)
				f := s.post
				if m[2] == "reel" {
					f = fx{}
					for k, v := range s.post {
						f[k] = v
					}
					f["kind"] = "reel"
				}
				page := s.postPage
				if page == "" {
					page = "post.html"
				}
				return html(render(t, page, f, map[string]string{"CODE": m[3], "AUTHOR": s.postAuthor}))
			case profilePath.MatchString(p):
				user := profilePath.FindStringSubmatch(p)[1]
				f, ok := s.profiles[user]
				if !ok {
					f = fx{"state": "follow", "message": true}
				}
				return html(render(t, "profile.html", f, map[string]string{"USER": user}))
			}
			return bottest.Response{Status: 404, Body: "not found"}
		}},
	)
	return page, rec
}

// posts returns the recorded POSTs whose URL matches the glob.
func posts(rec *bottest.Recorder, pattern string) []bottest.Request {
	return rec.Matching("POST", pattern)
}

// apiPosts lists every recorded POST path (for failure messages).
func apiPosts(rec *bottest.Recorder) []string {
	var out []string
	for _, r := range rec.Requests() {
		if r.Method == "POST" {
			u, _ := url.Parse(r.URL)
			out = append(out, u.Path+"?"+u.RawQuery+" "+r.PostData)
		}
	}
	return out
}

func resultMap(t *testing.T, res interface{}) map[string]interface{} {
	t.Helper()
	m, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("result is %T, want map: %#v", res, res)
	}
	return m
}

func resultList(t *testing.T, res interface{}) []map[string]interface{} {
	t.Helper()
	l, ok := res.([]interface{})
	if !ok {
		t.Fatalf("result is %T, want list: %#v", res, res)
	}
	out := make([]map[string]interface{}, len(l))
	for i, v := range l {
		out[i] = v.(map[string]interface{})
	}
	return out
}

func call(t *testing.T, page *bottest.Page, method string, args ...interface{}) (interface{}, error) {
	t.Helper()
	return bottest.CallMethod(t, New(), page, method, args...)
}
