package main

import (
	"strings"
	"testing"
)

// `ref tasks` says where a task from the Chrome extension lands (task board spec 11, 15.2).
func TestRefTasksSaysWhereChromeTasksLand(t *testing.T) {
	for _, want := range []string{"FROM CHROME", "MonoAgent Bridge", "Saving into", "Inbox"} {
		if !strings.Contains(refTasksText, want) {
			t.Errorf("`ref tasks` does not mention %q", want)
		}
	}
}
