package monomind

import (
	"encoding/json"
	"strings"
	"testing"
)

// The orgs folder holds more than orgs, and monomind's lister takes every
// .json in it — so `org list` offered ".mcp" (from a .mcp.json tool config)
// as an org with no roles. It sorts before every letter, so the GUI selected
// it by default and showed "no root role — exactly one role must have
// reports_to: null" constantly, while every real org was fine.
func TestDropUnnamableOrgsRemovesPhantoms(t *testing.T) {
	raw := json.RawMessage(`{"v":1,"items":[
		{"name":".mcp","roles":0,"status":"invalid-config","goal":""},
		{"name":"monomind-dev","roles":8,"status":"running","goal":"ship it"},
		{"name":"sample-team","roles":3,"status":"never run","goal":""}
	]}`)

	var got struct {
		V     int `json:"v"`
		Items []struct {
			Name  string `json:"name"`
			Roles int    `json:"roles"`
			Goal  string `json:"goal"`
		} `json:"items"`
	}
	if err := json.Unmarshal(dropUnnamableOrgs(raw), &got); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}

	if got.V != 1 {
		t.Errorf("envelope fields must survive, v = %d", got.V)
	}
	if len(got.Items) != 2 {
		t.Fatalf("want the two real orgs, got %+v", got.Items)
	}
	for _, item := range got.Items {
		if item.Name == ".mcp" {
			t.Fatal(".mcp is not an org and must not be listed")
		}
	}
	// Each surviving item keeps every field it arrived with.
	if got.Items[0].Name != "monomind-dev" || got.Items[0].Roles != 8 || got.Items[0].Goal != "ship it" {
		t.Errorf("item was altered: %+v", got.Items[0])
	}
}

func TestDropUnnamableOrgsLeavesGoodListsAlone(t *testing.T) {
	raw := json.RawMessage(`{"v":1,"items":[{"name":"growth","roles":2}]}`)
	if got := string(dropUnnamableOrgs(raw)); got != string(raw) {
		t.Fatalf("a clean list must be passed through byte for byte:\n got %s\nwant %s", got, raw)
	}
}

// This filters one known bad entry; it does not police the protocol. Output
// it cannot read is forwarded untouched, so an unexpected shape can never
// turn into a silently empty org list.
func TestDropUnnamableOrgsPassesThroughWhatItCannotRead(t *testing.T) {
	for _, raw := range []string{
		`not json at all`,
		`{"v":1}`,                       // no items
		`{"v":1,"items":"unexpected"}`,  // items not an array
		`{"v":1,"items":[{"roles":1}]}`, // an item with no name
		`{"v":1,"items":[42,"str"]}`,    // items that are not objects
		`[]`,                            // not the documented envelope
	} {
		if got := string(dropUnnamableOrgs(json.RawMessage(raw))); got != raw {
			t.Errorf("input %s was rewritten to %s", raw, got)
		}
	}
}

// The names monomind itself refuses to act on are exactly the ones dropped.
func TestDropUnnamableOrgsMatchesTheNameRule(t *testing.T) {
	cases := map[string]bool{ // name -> kept
		"growth": true, "docs-team": true, "UPPER_9": true,
		".mcp": false, ".claude": false, "-dash": false, "has space": false, "weird@name": false,
	}
	for name, keep := range cases {
		raw := json.RawMessage(`{"v":1,"items":[{"name":` + mustJSON(name) + `}]}`)
		out := string(dropUnnamableOrgs(raw))
		if got := strings.Contains(out, mustJSON(name)); got != keep {
			t.Errorf("name %q: kept = %v, want %v (output %s)", name, got, keep, out)
		}
	}
}

func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
