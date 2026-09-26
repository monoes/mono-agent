package action

import (
	"strings"
	"testing"
)

// export_followers declares its profile as a required target_url; configs
// written before that (profileUrl, targetUsername, or the node's targets
// list) must still satisfy it and fill target_url for the step templates.
func TestRequiredInputFilledFromAlias(t *testing.T) {
	for _, platform := range []string{"instagram", "x"} {
		def, err := GetLoader().Load(platform, "export_followers")
		if err != nil {
			t.Fatalf("loading %s export_followers: %v", platform, err)
		}
		cases := []struct {
			name string
			vars map[string]interface{}
			want string
		}{
			{"target_url", map[string]interface{}{"target_url": "https://x.test/a", "profileUrl": "https://x.test/b"}, "https://x.test/a"},
			{"profileUrl", map[string]interface{}{"profileUrl": "https://x.test/b"}, "https://x.test/b"},
			{"targetUsername", map[string]interface{}{"target_url": "", "targetUsername": "someone"}, "someone"},
			{"targets", map[string]interface{}{"targets": []interface{}{map[string]interface{}{"url": "https://x.test/c", "username": "c"}}}, "https://x.test/c"},
			{"selectedListItems strings", map[string]interface{}{"selectedListItems": []interface{}{"d"}}, "d"},
		}
		for _, c := range cases {
			ae, _, _ := newLoopTestExecutor(t, 0)
			for k, v := range c.vars {
				ae.SetVariable(k, v)
			}
			if err := ae.validateRequiredInputs(def); err != nil {
				t.Fatalf("%s/%s: %v", platform, c.name, err)
			}
			if got, _ := ae.execCtx.GetVariable("target_url"); got != c.want {
				t.Errorf("%s/%s: target_url = %v, want %q", platform, c.name, got, c.want)
			}
		}

		ae, _, _ := newLoopTestExecutor(t, 0)
		ae.SetVariable("targets", []interface{}{})
		err = ae.validateRequiredInputs(def)
		if err == nil || !strings.Contains(err.Error(), "target_url") {
			t.Errorf("%s: no profile at all: err = %v, want missing target_url", platform, err)
		}
	}
}

// instagram.list_user_posts takes its profile as target_url; configs that set
// targetUsername, profileUrl, the node's targets, or only username (the old form) keep
// working, and an explicit target wins over the session username.
func TestListUserPostsTargetAliases(t *testing.T) {
	def, err := GetLoader().Load("instagram", "list_user_posts")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		vars map[string]interface{}
		want string
	}{
		{"target_url", map[string]interface{}{"target_url": "https://www.instagram.com/a/", "username": "session"}, "https://www.instagram.com/a/"},
		{"targetUsername over username", map[string]interface{}{"targetUsername": "b", "username": "session"}, "b"},
		{"profileUrl over username", map[string]interface{}{"profileUrl": "https://www.instagram.com/e/", "username": "session"}, "https://www.instagram.com/e/"},
		{"targets over username", map[string]interface{}{"targets": []interface{}{map[string]interface{}{"url": "https://www.instagram.com/c/"}}, "username": "session"}, "https://www.instagram.com/c/"},
		{"username only (old form)", map[string]interface{}{"username": "d"}, "d"},
	}
	for _, c := range cases {
		ae, _, _ := newLoopTestExecutor(t, 0)
		for k, v := range c.vars {
			ae.SetVariable(k, v)
		}
		if err := ae.validateRequiredInputs(def); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got, _ := ae.execCtx.GetVariable("target_url"); got != c.want {
			t.Errorf("%s: target_url = %v, want %q", c.name, got, c.want)
		}
	}
	ae, _, _ := newLoopTestExecutor(t, 0)
	if err := ae.validateRequiredInputs(def); err == nil || !strings.Contains(err.Error(), "target_url") {
		t.Errorf("no profile: err = %v, want missing target_url", err)
	}
}
