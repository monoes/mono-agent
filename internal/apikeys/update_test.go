package apikeys

import "testing"

// An update that sets nothing asks for nothing: the CLI and the MCP tool refuse it
// in their own words, on this one rule. A field set to its zero value is still set:
// turning context off is a change.
func TestUpdateIsEmptyOnlyWhenItSetsNothing(t *testing.T) {
	name, empty, off, on := "app", "", false, true
	for _, c := range []struct {
		what string
		u    Update
		want bool
	}{
		{"nothing", Update{}, true},
		{"a name", Update{Name: &name}, false},
		{"an empty name, which is invalid, not absent", Update{Name: &empty}, false},
		{"context on", Update{Context: &on}, false},
		{"context off", Update{Context: &off}, false},
		{"both", Update{Name: &name, Context: &off}, false},
	} {
		if got := c.u.IsEmpty(); got != c.want {
			t.Errorf("%s: IsEmpty() = %v, want %v", c.what, got, c.want)
		}
	}
}
