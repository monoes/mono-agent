package main

import (
	"encoding/json"
	"testing"
)

// `--profile Work workflow import` must save into Work, whatever profile
// id (often none) the file carries. It used to keep the file's, so the
// workflow landed in default.
func TestWorkflowImportSavesIntoTheTargetProfile(t *testing.T) {
	cfg := twoProfileDB(t) // profiles default and p-work
	work := *cfg
	work.ProfileID = "p-work"
	id := importWorkflowJSON(t, &work, `{"name":"imported","profile_id":"","nodes":[{"id":"t","type":"trigger.manual","name":"T","position":{"x":0,"y":0},"config":{}}],"connections":[]}`)

	var rows []struct {
		ID        string `json:"id"`
		ProfileID string `json:"profile_id"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "list", "--all-profiles")), &rows); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.ID == id {
			found = true
			if r.ProfileID != "p-work" {
				t.Fatalf("imported into %q, want p-work", r.ProfileID)
			}
		}
	}
	if !found {
		t.Fatalf("imported workflow %s not listed: %+v", id, rows)
	}
}
