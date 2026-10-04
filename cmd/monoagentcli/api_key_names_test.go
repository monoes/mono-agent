package main

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apikeys"
)

// A key pasted as a name is refused, by create and by update, as invalid input
// (exit 3): the name would be stored in clear and shown by `api key list` and by
// every other front end. The message does not repeat what was passed.
func TestAPIKeyNameThatIsAKeyIsRefusedWithExit3(t *testing.T) {
	db := newAPITestDB(t)
	secret, err := apikeys.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runAPI(t, db, "default", true, "key", "create", "--name", secret)
	if exitCode(err) != 3 {
		t.Fatalf("create with a key as the name: exit %d, want 3", exitCode(err))
	}
	if strings.Contains(stdout+stderr+err.Error(), secret) {
		t.Error("create printed the key it was refused")
	}

	if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", "app"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err = runAPI(t, db, "default", true, "key", "update", "app", "--name", secret)
	if exitCode(err) != 3 {
		t.Fatalf("update to a key as the name: exit %d, want 3", exitCode(err))
	}
	if strings.Contains(stdout+stderr+err.Error(), secret) {
		t.Error("update printed the key it was refused")
	}

	list, _, err := runAPI(t, db, "default", true, "key", "list", "--include-revoked")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list, secret) || strings.Count(list, `"name"`) != 1 {
		t.Error("a refused name reached the stored keys")
	}
}
