//go:build social

package linkedin

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/browser"
)

func TestGetProfileData(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)

	t.Run("classic top card", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		res, err := call(t, &LinkedInBot{}, p, "get_profile_data", "https://www.linkedin.com/in/ada-first-test/")
		if err != nil {
			t.Fatal(err)
		}
		m := res.(map[string]interface{})
		want := map[string]interface{}{
			"username": "ada-first-test", "profile_url": "https://www.linkedin.com/in/ada-first-test/",
			"full_name": "Ada First", "headline": "Robotics engineer at Example Works", "location": "Lisbon, Portugal",
			"connection_degree": "1st", "connection_count": "500+", "follower_count": "1,234",
			"about": "About Ada First: builds things.", "is_self": false,
		}
		for k, v := range want {
			if m[k] != v {
				t.Errorf("%s = %v, want %v", k, m[k], v)
			}
		}
		if pic, _ := m["profile_picture_url"].(string); !strings.HasPrefix(pic, "data:image/gif") {
			t.Errorf("profile_picture_url = %v", m["profile_picture_url"])
		}
		if w := writes(rec); len(w) != 0 {
			t.Fatalf("a read made writes: %v", w)
		}
	})

	t.Run("server-driven top card (own profile)", func(t *testing.T) {
		p, _ := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/in/*", File: "testdata/profile_sdui.html"})
		res, err := call(t, &LinkedInBot{}, p, "get_profile_data", "kit-sample-test")
		if err != nil {
			t.Fatal(err)
		}
		m := res.(map[string]interface{})
		want := map[string]interface{}{
			"username": "kit-sample-test", "full_name": "Kit Sample", "headline": "Founder at Sample Studio",
			"location": "Porto, Portugal", "connection_count": "500+", "follower_count": "3,210",
			"about": "I build calm tools for busy teams.", "is_self": true,
			"profile_picture_url": "https://media.licdn.com/dms/image/v2/fake/profile-displayphoto.jpg",
		}
		for k, v := range want {
			if m[k] != v {
				t.Errorf("%s = %v, want %v", k, m[k], v)
			}
		}
	})

	t.Run("nothing rendered is an error", func(t *testing.T) {
		p, _ := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/in/*", Body: `<!doctype html><main><p>loading…</p></main>`})
		if _, err := call(t, &LinkedInBot{}, p, "get_profile_data", "https://www.linkedin.com/in/slow-test/"); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestIsLoggedIn(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	p, _ := newPage(t, b, profileRoute,
		bottest.Route{Pattern: "https://www.linkedin.com/login*", Body: `<!doctype html><form class="login__form"><input id="username" name="session_key"></form>`})
	var page browser.PageInterface = p
	if err := p.Navigate("https://www.linkedin.com/in/ada-first-test/"); err != nil {
		t.Fatal(err)
	}
	if ok, err := (&LinkedInBot{}).IsLoggedIn(page); err != nil || !ok {
		t.Fatalf("profile page: IsLoggedIn = %v, %v", ok, err)
	}
	if err := p.Navigate("https://www.linkedin.com/login"); err != nil {
		t.Fatal(err)
	}
	if ok, err := (&LinkedInBot{}).IsLoggedIn(page); err != nil || ok {
		t.Fatalf("login page: IsLoggedIn = %v, %v", ok, err)
	}
}

func TestSearchPeople(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	route := bottest.Route{Pattern: "https://www.linkedin.com/search/results/people/*", File: "testdata/search_people.html"}

	t.Run("pages until max", func(t *testing.T) {
		p, rec := newPage(t, b, route)
		res, err := call(t, &LinkedInBot{}, p, "search_people", "workflow automation", 4)
		if err != nil {
			t.Fatal(err)
		}
		people := res.([]map[string]interface{})
		var names []string
		for _, x := range people {
			names = append(names, x["full_name"].(string))
		}
		if got := strings.Join(names, ","); got != "Lena Ortiz,Omar Quint,Pia Vale,Rui Stone" {
			t.Fatalf("names = %s", got)
		}
		first := people[0]
		if first["url"] != "https://www.linkedin.com/in/lena-ortiz-test/" || first["username"] != "lena-ortiz-test" ||
			first["headline"] != "Workflow automation consultant" || first["location"] != "Berlin, Germany" || first["connection_degree"] != "2nd" {
			t.Fatalf("first = %v", first)
		}
		// Rui Stone's card has no photo: the name must not carry " • 2nd".
		if rui := people[3]; rui["full_name"] != "Rui Stone" || rui["connection_degree"] != "2nd" || rui["headline"] != "No-code builder" || rui["location"] != "Porto, Portugal" {
			t.Fatalf("fourth = %v", rui)
		}
		if len(rec.Matching("GET", "https://www.linkedin.com/search/results/people/?keywords=workflow+automation&origin=GLOBAL_SEARCH_HEADER")) != 1 ||
			len(rec.Matching("GET", "https://www.linkedin.com/search/results/people/?*page=2*")) != 1 {
			t.Fatalf("requests = %v", rec.Requests())
		}
	})

	t.Run("stops when a page is empty", func(t *testing.T) {
		p, _ := newPage(t, b, route)
		res, err := call(t, &LinkedInBot{}, p, "search_people", "https://www.linkedin.com/search/results/people/?keywords=rpa", 50)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(res.([]map[string]interface{})); n != 5 {
			t.Fatalf("got %d people, want 5", n)
		}
	})
}

func TestListFollowers(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	route := bottest.Route{Pattern: "https://www.linkedin.com/mynetwork/network-manager/people-follow/*", File: "testdata/followers.html"}

	t.Run("followers, show more", func(t *testing.T) {
		p, rec := newPage(t, b, route)
		res, err := call(t, &LinkedInBot{}, p, "list_followers", "FOLLOWERS_FETCH", "")
		if err != nil {
			t.Fatal(err)
		}
		people := res.([]map[string]interface{})
		if len(people) != 5 {
			t.Fatalf("got %d: %v", len(people), people)
		}
		if people[0]["full_name"] != "Ann Follower" || people[0]["url"] != "https://www.linkedin.com/in/ann-fol-test" ||
			people[0]["headline"] != "Data analyst at Example Bank" || people[0]["you_follow"] != true || people[1]["you_follow"] != false {
			t.Fatalf("people = %v", people[:2])
		}
		// Ann and Ben show a presence dot ("Status is online"/"away"): its
		// screen-reader label is neither a headline nor a location.
		for i, head := range []string{"Data analyst at Example Bank", "Student", "Barista"} {
			if people[i]["headline"] != head || people[i]["location"] != "" {
				t.Errorf("person %d headline %q location %q, want %q and none", i, people[i]["headline"], people[i]["location"], head)
			}
		}
		if len(rec.Matching("POST", "*/follow")) != 0 {
			t.Fatal("a follow button was pressed")
		}
	})

	t.Run("following, max", func(t *testing.T) {
		p, rec := newPage(t, b, route)
		res, err := call(t, &LinkedInBot{}, p, "list_followers", "following", 2)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(res.([]map[string]interface{})); n != 2 {
			t.Fatalf("got %d", n)
		}
		if len(rec.Matching("GET", "https://www.linkedin.com/mynetwork/network-manager/people-follow/following/")) != 1 {
			t.Fatalf("requests = %v", rec.Requests())
		}
	})

	t.Run("bad source type", func(t *testing.T) {
		p, _ := newPage(t, b, route)
		if _, err := call(t, &LinkedInBot{}, p, "list_followers", "LIKERS", 2); err == nil {
			t.Fatal("want error")
		}
	})
}
