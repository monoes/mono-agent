package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The message bindings go through `people messages …`, not the database.
func TestPersonMessageBindingsShellOut(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
case "$*" in
  *" messages list "*) echo '[{"id":"m1","person_id":"p1","direction":"inbound","source":"x","created_at":"2026-09-26T10:00:00Z"}]' ;;
  *" messages all "*) echo '[{"id":"m2","person_id":"p1","direction":"inbound","source":"x","created_at":"2026-09-26T10:00:00Z","read_at":"2026-09-26T11:00:00Z","person_platform_username":"a","person_platform":"x"}]' ;;
  *) echo '{}' ;;
esac
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")
	if m := a.GetPersonMessages("p1"); len(m) != 1 || m[0].ID != "m1" || m[0].ReadAt != nil {
		t.Fatalf("GetPersonMessages = %+v", m)
	}
	if m := a.GetAllPersonMessages(50); len(m) != 1 || m[0].ReadAt == nil {
		t.Fatalf("GetAllPersonMessages = %+v", m)
	}
	if err := a.MarkPersonMessagesRead("p1", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.MarkPersonMessagesRead("", []string{"m1", "m2"}); err != nil {
		t.Fatal(err)
	}
	if err := a.MarkPersonMessageUnread("m1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--profile work --json people messages list p1 --limit 100",
		"--profile work --json people messages all --limit 50",
		"--profile work --json people messages read --person p1",
		"--profile work --json people messages read m1 m2",
		"--profile work --json people messages unread m1",
	}
	if got := loggedArgs(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("argv:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
