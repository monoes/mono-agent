//go:build social

package linkedin

import "testing"

// Live bug: a company post's author_url pointed at the company's /posts tab.
func TestCanonicalEntityURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.linkedin.com/company/mon0/posts":                   "https://www.linkedin.com/company/mon0",
		"https://www.linkedin.com/company/mon0/about/":                  "https://www.linkedin.com/company/mon0",
		"https://www.linkedin.com/company/mon0/":                        "https://www.linkedin.com/company/mon0/",
		"https://www.linkedin.com/in/owen-owner-test":                   "https://www.linkedin.com/in/owen-owner-test",
		"https://www.linkedin.com/in/jane-doe-123/recent-activity/all/": "https://www.linkedin.com/in/jane-doe-123",
		"": "",
	} {
		if got := canonicalEntityURL(in); got != want {
			t.Errorf("canonicalEntityURL(%q) = %q, want %q", in, got, want)
		}
	}
}
