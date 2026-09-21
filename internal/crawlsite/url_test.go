package crawlsite

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://Example.COM/a/b?x=1#frag", "https://example.com/a/b?x=1"},
		{"https://example.com:443/a", "https://example.com/a"},
		{"http://example.com:80", "http://example.com/"},
		{"https://example.com/p?utm_source=news&id=7&fbclid=zz", "https://example.com/p?id=7"},
		{"https://example.com/p?b=2&a=1", "https://example.com/p?a=1&b=2"},
		{"https://user:pw@example.com/p", "https://example.com/p"},
	}
	for _, tc := range cases {
		got, err := normalizeURL(tc.in)
		if err != nil {
			t.Fatalf("normalize %q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("normalize %q = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeURLRejects(t *testing.T) {
	for _, in := range []string{"ftp://example.com/x", "mailto:a@b.c", "/relative/only", "javascript:alert(1)"} {
		if got, err := normalizeURL(in); err == nil {
			t.Errorf("normalize %q = %q, want an error", in, got)
		}
	}
}

func TestScope(t *testing.T) {
	s := newScope(false)
	s.add("Example.com")
	if !s.allows("example.com") {
		t.Error("exact host should be in scope")
	}
	if s.allows("docs.example.com") {
		t.Error("subdomain should be out of scope without includeSubdomains")
	}
	if s.allows("evil-example.com") {
		t.Error("unrelated host should be out of scope")
	}

	sub := newScope(true)
	sub.add("example.com:8080")
	if !sub.allows("docs.example.com") {
		t.Error("subdomain should be in scope with includeSubdomains")
	}
	if sub.allows("notexample.com") {
		t.Error("suffix match must be on a dot boundary")
	}
}
