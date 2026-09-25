package browser

import "testing"

func TestStartURL(t *testing.T) {
	t.Cleanup(func() { SetStartURLResolver(nil) })

	SetStartURLResolver(nil)
	if got := StartURL("Instagram"); got != "https://www.instagram.com" {
		t.Errorf("builtin: got %q", got)
	}
	if got := StartURL("acme-crm"); got != "about:blank" {
		t.Errorf("unknown: got %q", got)
	}

	SetStartURLResolver(func(p string) string {
		if p == "acme-crm" {
			return "https://app.acme-crm.com/"
		}
		return ""
	})
	if got := StartURL("acme-crm"); got != "https://app.acme-crm.com/" {
		t.Errorf("package: got %q", got)
	}
	if got := StartURL("x"); got != "https://x.com" {
		t.Errorf("fallback: got %q", got)
	}
}
