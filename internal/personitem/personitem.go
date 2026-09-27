// Package personitem decides which person an extracted item is about and
// which people columns it fills. It is shared by every path that turns
// extracted items into people rows (the browser nodes' SaveExtractedData,
// people.save, `node run`), so they agree on both.
//
// A person is only ever made from a URL that names a person. A platform
// with a bot adapter decides that through the adapter's ExtractUsername: a
// URL it doesn't recognise as a profile (a post permalink, a company page)
// names nobody. An item about a post or comment can still name its author
// (author_url), and then the author is the person. Platforms without an
// adapter fall back to the URL's last path segment, except for URLs that
// look like posts, feeds or statuses.
package personitem

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/monoes/mono-agent/internal/bot"
)

// Ref is the person an item resolves to.
type Ref struct {
	Username   string
	ProfileURL string
	// Author is true when the item is about something the person wrote (a
	// post, a comment) rather than about the person: only FullName then
	// describes the person, the item's other fields describe the post.
	Author   bool
	FullName string
}

// subjectURLKeys name the item's own URL, in priority order.
var subjectURLKeys = []string{"profile_url", "url"}

// authorURLKeys name the URL of the item's author.
var authorURLKeys = []string{"author_url", "author_profile_url"}

// Resolve returns the person item is about, or false when no URL on it
// names a person.
func Resolve(platform string, item map[string]interface{}) (Ref, bool) {
	for _, k := range subjectURLKeys {
		u := str(item, k)
		if name := Username(platform, u); name != "" {
			return Ref{Username: name, ProfileURL: u}, true
		}
	}
	for _, k := range authorURLKeys {
		u := str(item, k)
		name := Username(platform, u)
		if name == "" {
			continue
		}
		full := firstString(item, "author_name", "author")
		if strings.EqualFold(strings.TrimPrefix(full, "@"), name) {
			full = "" // the author field holds the handle, not a name
		}
		return Ref{Username: name, ProfileURL: u, Author: true, FullName: full}, true
	}
	return Ref{}, false
}

// Username returns the username a profile URL names on platform, or "".
func Username(platform, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if factory, ok := bot.PlatformRegistry[strings.ToUpper(platform)]; ok {
		return clean(factory().ExtractUsername(rawURL))
	}
	return genericUsername(rawURL)
}

// placeholderUsernames are values adapters return for platforms without
// profiles; they name nobody.
var placeholderUsernames = map[string]bool{"gemini-user": true}

func clean(u string) string {
	u = strings.TrimPrefix(strings.TrimSpace(u), "@")
	if u == "" || placeholderUsernames[u] || strings.ContainsAny(u, ":/?# \t\n") {
		return ""
	}
	return u
}

// nonProfileSegments are path segments of URLs that are about content or
// organisations, not people, on the sites mono-agent reads.
var nonProfileSegments = map[string]bool{
	"status": true, "statuses": true, "p": true, "reel": true, "reels": true, "tv": true,
	"posts": true, "post": true, "feed": true, "update": true, "updates": true, "activity": true,
	"video": true, "videos": true, "photo": true, "photos": true, "item": true, "items": true,
	"watch": true, "shorts": true, "comments": true, "comment": true, "pulse": true,
	"article": true, "articles": true, "company": true, "school": true, "groups": true,
	"hashtag": true, "tag": true, "tags": true, "explore": true, "search": true, "events": true,
	"jobs": true, "launches": true, "products": true, "share": true, "i": true,
}

// genericUsername is the last path segment of a URL that doesn't look like
// a post, feed or organisation page.
func genericUsername(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return ""
	}
	segs := strings.Split(path, "/")
	for _, s := range segs {
		if nonProfileSegments[strings.ToLower(s)] {
			return ""
		}
	}
	last := clean(segs[len(segs)-1])
	if last == "" || strings.Trim(last, "0123456789") == "" {
		return "" // a numeric id is an item, not a handle
	}
	return last
}

// Profile holds the people columns an item describing a person fills.
// Empty fields leave the stored value alone.
type Profile struct {
	FullName   string
	ImageURL   string
	Website    string
	JobTitle   string
	Headline   string
	Location   string
	About      string
	Experience string // JSON array
	Education  string // JSON array
}

// ProfileOf maps an item's fields, as the platforms name them, onto people
// columns. job_title is the item's own job_title, else its headline.
func ProfileOf(item map[string]interface{}) Profile {
	p := Profile{
		FullName: firstString(item, "full_name", "name"),
		ImageURL: firstString(item, "image_url", "profile_picture_url", "profile_pic_url", "avatar_url"),
		Website:  firstString(item, "website"),
		Headline: firstString(item, "headline"),
		Location: firstString(item, "location"),
		About:    firstString(item, "about", "bio"),
	}
	p.JobTitle = firstString(item, "job_title", "position")
	if p.JobTitle == "" {
		p.JobTitle = p.Headline
	}
	p.Experience = JSONList(item["experience"])
	p.Education = JSONList(item["education"])
	return p
}

// JSONList renders a list value as JSON text, or "" when it is absent or
// empty. A string is taken to already be JSON and kept when it is an array.
func JSONList(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		var arr []interface{}
		if json.Unmarshal([]byte(t), &arr) != nil || len(arr) == 0 {
			return ""
		}
		return t
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var arr []interface{}
	if json.Unmarshal(b, &arr) != nil || len(arr) == 0 {
		return ""
	}
	return string(b)
}

func str(m map[string]interface{}, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func firstString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if s := str(m, k); s != "" {
			return s
		}
	}
	return ""
}
