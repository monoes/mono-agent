//go:build social

package instagram

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
)

// Jev fallback: when the bot's own finder cannot recognise a control, Jev
// (here the jevtest fake, no network) picks one from the page snapshot. The
// bot validates every pick before acting on it.

// pickBy answers the "target" question with the option whose element label
// matches re — the first match, or the last when last is set.
func pickBy(t *testing.T, re *regexp.Regexp, last bool) jevtest.Handler {
	return func(req jev.Request) map[string]string {
		q, ok := req.Questions["target"]
		if !ok {
			return nil
		}
		crit, _ := q.Criteria.(map[string]any)
		var ids []string
		for id := range crit {
			if id != "NONE" {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool { a, _ := strconv.Atoi(ids[i]); b, _ := strconv.Atoi(ids[j]); return a < b })
		var hits []string
		var labels []string
		for _, id := range ids {
			c, _ := crit[id].(map[string]any)
			el := fmt.Sprint(c["element"])
			labels = append(labels, el)
			if re.MatchString(el) {
				hits = append(hits, id)
			}
		}
		t.Logf("jev options: %s", strings.Join(labels, " | "))
		if len(hits) == 0 {
			return map[string]string{"target": "NONE"}
		}
		if last {
			return map[string]string{"target": hits[len(hits)-1]}
		}
		return map[string]string{"target": hits[0]}
	}
}

func jevBot(t *testing.T, h jevtest.Handler) (*InstagramBot, *jevtest.Server) {
	t.Helper()
	srv := jevtest.NewServer(t, h)
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	b := New()
	b.SetJevPicker(c, 0)
	return b, srv
}

func TestJevLikePostPicksThePostHeart(t *testing.T) {
	s := newSite()
	s.post["oddBar"] = true
	s.post["hidden"] = []fx{}
	page, rec := s.open(t)
	b, srv := jevBot(t, pickBy(t, regexp.MustCompile(`\] Like$`), true))
	res, err := bottest.CallMethod(t, b, page, "like_post", postURL)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Calls() == 0 {
		t.Fatal("Jev was not consulted")
	}
	if m := resultMap(t, res); m["status"] != "liked" {
		t.Errorf("result = %v", m)
	}
	if n := len(posts(rec, "*/likes/FXMEDIA1/like/")); n != 1 {
		t.Errorf("post likes: %v", apiPosts(rec))
	}
}

func TestJevLikePostRefusesACommentHeart(t *testing.T) {
	s := newSite()
	s.post["oddBar"] = true
	page, rec := s.open(t)
	b, _ := jevBot(t, pickBy(t, regexp.MustCompile(`\] Like$`), false)) // the first heart: a comment's
	if _, err := bottest.CallMethod(t, b, page, "like_post", postURL); err == nil {
		t.Fatal("a comment heart picked by Jev must be refused")
	}
	if p := apiPosts(rec); len(p) != 0 {
		t.Errorf("clicked: %v", p)
	}
}

func TestJevLikePostWithoutPickerFails(t *testing.T) {
	s := newSite()
	s.post["oddBar"] = true
	page, rec := s.open(t)
	if _, err := call(t, page, "like_post", postURL); err == nil {
		t.Fatal("no recognisable Like button and no Jev: must fail")
	}
	if p := apiPosts(rec); len(p) != 0 {
		t.Errorf("clicked: %v", p)
	}
}

func TestJevFollowUserPicksHeaderControl(t *testing.T) {
	s := newSite()
	s.profiles["fake.odd"] = fx{"state": "follow", "oddFollow": true}
	page, rec := s.open(t)
	b, srv := jevBot(t, pickBy(t, regexp.MustCompile(`\] Follow$`), false))
	res, err := bottest.CallMethod(t, b, page, "follow_user", "fake.odd")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Calls() == 0 {
		t.Fatal("Jev was not consulted")
	}
	if m := resultMap(t, res); m["status"] != "followed" {
		t.Errorf("result = %v", m)
	}
	if n := len(posts(rec, "*/friendships/create/9001/")); n != 1 {
		t.Errorf("follows: %v", apiPosts(rec))
	}
}

func TestJevFollowUserRefusesASuggestion(t *testing.T) {
	s := newSite()
	s.profiles["fake.odd"] = fx{"state": "follow", "oddFollow": true}
	page, rec := s.open(t)
	b, _ := jevBot(t, pickBy(t, regexp.MustCompile(`\] Follow$`), true)) // the last Follow: a suggestion
	if _, err := bottest.CallMethod(t, b, page, "follow_user", "fake.odd"); err == nil {
		t.Fatal("a 'Suggested for you' Follow picked by Jev must be refused")
	}
	if p := apiPosts(rec); len(p) != 0 {
		t.Errorf("followed someone: %v", p)
	}
}

func TestJevCommentBox(t *testing.T) {
	s := newSite()
	s.post["oddBox"] = true
	page, rec := s.open(t)
	b, srv := jevBot(t, pickBy(t, regexp.MustCompile(`(?i)say something nice`), false))
	if _, err := bottest.CallMethod(t, b, page, "comment_post", postURL, "found the box"); err != nil {
		t.Fatal(err)
	}
	if srv.Calls() == 0 {
		t.Fatal("Jev was not consulted")
	}
	got := posts(rec, "*/comments/FXMEDIA1/add/")
	if len(got) != 1 || form(t, got[0]).Get("comment_text") != "found the box" {
		t.Errorf("comments: %v", apiPosts(rec))
	}
}

func TestInstagramBotExposesJevSetter(t *testing.T) {
	var a interface{} = New()
	if _, ok := a.(interface{ SetJevPicker(*jev.Client, float64) }); !ok {
		t.Fatal("InstagramBot must expose SetJevPicker")
	}
}
