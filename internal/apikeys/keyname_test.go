package apikeys

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A key must never become a name. A name is stored in clear and listed by every
// front end (the CLI, the GUI, the read-only MCP tool), so a key pasted as a name
// would be shown to whoever can list keys, against the rule that only its hash is
// kept. The alphabet of a name holds every character of a key, so the rule has to
// say it: nothing may hold the start of one, not even a key cut short.
func TestAKeyIsNotAName(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	secret, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keyed, _, err := s.Create(ctx, "default", "app", false)
	if err != nil {
		t.Fatal(err)
	}

	for what, bad := range map[string]string{
		"a key":                       secret,
		"a key in capitals":           strings.ToUpper(secret),
		"the start of every key":      "sk-ma-",
		"the start in capitals":       "SK-MA-x",
		"a key after other words":     "my " + secret,
		"a key before other words":    secret + " backup",
		"a key cut short":             secret[:len(secret)-1],
		"the prefix after other text": "prod sk-ma-abc",
	} {
		_, _, err := s.Create(ctx, "default", bad, false)
		if !errors.Is(err, ErrInvalidName) {
			t.Errorf("Create with %s as the name: err = %v, want ErrInvalidName", what, err)
		} else if strings.Contains(err.Error(), secret[len(KeyPrefix):]) {
			t.Errorf("the error for %s repeats the name", what)
		}
		if _, err := s.Update(ctx, "default", keyed.ID, Update{Name: &bad}); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Update to %s as the name: err = %v, want ErrInvalidName", what, err)
		}
	}

	// Nothing was stored: the list shows the one real name, and no key.
	keys, err := s.List(ctx, "default", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Name != "app" {
		// Not printed: what a bug stored is the very thing that must not be shown.
		t.Errorf("a refused name changed the keys (%d of them)", len(keys))
	}

	// A name that only resembles one is fine.
	for _, ok := range []string{"sk-ma", "disk-manager", "sk", "ma-sk-", "my-key"} {
		if _, _, err := s.Create(ctx, "default", ok, false); err != nil {
			t.Errorf("Create(%q): %v, want it accepted", ok, err)
		}
	}
}
