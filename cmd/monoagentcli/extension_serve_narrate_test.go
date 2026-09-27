package main

import (
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/extension"
)

func TestNarrateBrowsers(t *testing.T) {
	t0 := time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC)
	work := extension.ConnInfo{Instance: "aaaaaaaa-1", Label: "Edge Work", Profile: "p-work", ConnectedAt: t0}
	home := extension.ConnInfo{Instance: "bbbbbbbb-2", ConnectedAt: t0}
	explained := false

	got := narrateBrowsers(nil, []extension.ConnInfo{work, home}, &explained)
	if len(got) != 2 || got[0] != "extension connected: Edge Work (profile p-work)" || got[1] != "extension connected: bbbbbbbb (any profile)" {
		t.Fatalf("connect lines = %q", got)
	}

	again := work
	again.ConnectedAt = t0.Add(time.Minute)
	got = narrateBrowsers([]extension.ConnInfo{work, home}, []extension.ConnInfo{again}, &explained)
	if len(got) != 2 || got[0] != "extension reconnected: Edge Work (profile p-work)" ||
		!strings.HasPrefix(got[1], "extension disconnected: bbbbbbbb (any profile) — normal:") {
		t.Fatalf("reconnect/disconnect lines = %q", got)
	}

	got = narrateBrowsers([]extension.ConnInfo{again}, nil, &explained)
	if len(got) != 1 || got[0] != "extension disconnected: Edge Work (profile p-work)" {
		t.Fatalf("second disconnect is not explained again: %q", got)
	}
	if got := narrateBrowsers([]extension.ConnInfo{again}, []extension.ConnInfo{again}, &explained); len(got) != 0 {
		t.Fatalf("no change, no lines: %q", got)
	}
}
