package automation

import "testing"

func TestDisplayUsername(t *testing.T) {
	for in, want := range map[string]string{"unknown": "", " Unknown ": "", "": "", "jack": "jack"} {
		if got := DisplayUsername(in); got != want {
			t.Errorf("DisplayUsername(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeLoginUsername(t *testing.T) {
	for in, want := range map[string]string{
		"/jack":                                "jack",
		"/@jack":                               "jack",
		"/@jack/":                              "jack",
		"https://x.com/jack":                   "jack",
		"https://www.tiktok.com/@jack?lang=en": "jack",
		"  @jack ":                             "jack",
		"Jack Dorsey":                          "Jack Dorsey",
		"/":                                    "",
		"":                                     "",
	} {
		if got := NormalizeLoginUsername(in); got != want {
			t.Errorf("NormalizeLoginUsername(%q) = %q, want %q", in, got, want)
		}
	}
}
