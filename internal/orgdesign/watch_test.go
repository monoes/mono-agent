package orgdesign

import (
	"sync"
	"testing"
	"time"
)

func TestWatcher_DetectsCreateUpdateDelete(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var got []Change
	w := NewWatcher(OrgsDir(dir), 30*time.Millisecond, func(c Change) {
		mu.Lock()
		got = append(got, c)
		mu.Unlock()
	})
	w.Start()
	defer w.Stop()

	d := NewOrg("watched-org", "goal", NewOrgOptions{})
	if _, err := Save(dir, d); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range got {
			if c.Name == "watched-org" && !c.Deleted && c.Doc != nil {
				return true
			}
		}
		return false
	})

	// Update: add a role.
	d2, err := Load(dir, "watched-org")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d2.AddRole(Role{Title: "Worker", ReportsTo: strPtr("lead")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(dir, d2); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range got {
			if c.Name == "watched-org" && c.Doc != nil && len(c.Doc.Roles) == 2 {
				return true
			}
		}
		return false
	})

	// Delete.
	if err := Delete(dir, "watched-org"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range got {
			if c.Name == "watched-org" && c.Deleted {
				return true
			}
		}
		return false
	})
}

func TestWatcher_SelfWriteSuppressed(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var got []Change
	w := NewWatcher(OrgsDir(dir), 30*time.Millisecond, func(c Change) {
		mu.Lock()
		got = append(got, c)
		mu.Unlock()
	})
	w.Start()
	defer w.Stop()

	d := NewOrg("self-written", "goal", NewOrgOptions{})
	sha, err := Save(dir, d)
	if err != nil {
		t.Fatal(err)
	}
	w.MarkSelfWrite("self-written", sha)

	// Give the watcher several intervals to have observed and (correctly)
	// suppressed this write.
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	for _, c := range got {
		if c.Name == "self-written" {
			t.Fatalf("expected self-write to be suppressed, but got a change callback: %+v", c)
		}
	}
}

func TestWatcher_IgnoresArtifactFiles(t *testing.T) {
	dir := t.TempDir()
	// Save a real org and an artifact-suffixed sibling; only the real one
	// should ever produce a callback.
	d := NewOrg("real-org", "goal", NewOrgOptions{})
	if _, err := Save(dir, d); err != nil {
		t.Fatal(err)
	}
	// Manually write an artifact file with a matching prefix.
	artifactDoc := NewOrg("real-org-state", "irrelevant", NewOrgOptions{})
	if _, err := Save(dir, artifactDoc); err != nil {
		// This particular helper writes to <name>.json, so to truly emulate
		// an artifact we'd need "real-org-state.json" which IS what
		// NewOrg("real-org-state",...) produces via Save. Good — this file
		// name ends in "-state.json" and must be excluded from discovery.
		t.Fatal(err)
	}

	names, err := ListOrgNames(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "real-org" {
		t.Fatalf("ListOrgNames should exclude the artifact file, got %v", names)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
