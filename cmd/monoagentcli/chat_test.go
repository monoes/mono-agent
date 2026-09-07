package main

import (
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// TestParseToolsFlag covers the mechanical run-gate plumbing: --tools is a
// comma-separated member set where "runs" is only valid alongside
// "monoagent", and unknown members fail loudly.
func TestParseToolsFlag(t *testing.T) {
	cases := []struct {
		in              string
		monoagent, runs bool
		wantErr         bool
	}{
		{"", false, false, false},
		{"monoagent", true, false, false},
		{"monoagent,runs", true, true, false},
		{"runs,monoagent", true, true, false},
		{" monoagent , runs ", true, true, false},
		{"monoagent,", true, false, false},
		{"runs", false, false, true},
		{"bogus", false, false, true},
		{"monoagent,bogus", false, false, true},
	}
	for _, c := range cases {
		mono, runs, err := parseToolsFlag(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseToolsFlag(%q) = nil error, want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseToolsFlag(%q) unexpected error: %v", c.in, err)
			continue
		}
		if mono != c.monoagent || runs != c.runs {
			t.Errorf("parseToolsFlag(%q) = (%v, %v), want (%v, %v)", c.in, mono, runs, c.monoagent, c.runs)
		}
	}
}

// TestAppendNewToolSpecsDropsDuplicateNames is a regression test: when both
// CanvasTools and MonoagentTools are wired into a chat turn (the common case
// — see effectiveCanvasID's auto-wiring in the chat RunE, which activates
// canvas tools any time --tools monoagent is set, even without an explicit
// --canvas), both define a "create_workflow" tool. Sending the runtime two
// ToolSpecs with the same name isn't merely redundant, it's fatal — the
// runtime rejects the second registration outright ("Tool create_workflow
// is already registered"), which previously broke every single turn (even
// a plain "hi") once monoagent tools were enabled.
func TestAppendNewToolSpecsDropsDuplicateNames(t *testing.T) {
	existing := []monomind.ToolSpec{
		{Name: "create_workflow", Description: "canvas version"},
		{Name: "create_nodes", Description: "canvas only"},
	}
	adding := []monomind.ToolSpec{
		{Name: "create_workflow", Description: "monoagent version — must be dropped"},
		{Name: "list_workflows", Description: "monoagent only"},
	}
	got := appendNewToolSpecs(existing, adding)

	byName := make(map[string]monomind.ToolSpec, len(got))
	for _, ts := range got {
		if _, dup := byName[ts.Name]; dup {
			t.Fatalf("appendNewToolSpecs produced a duplicate %q entry: %+v", ts.Name, got)
		}
		byName[ts.Name] = ts
	}

	if len(got) != 3 {
		t.Fatalf("appendNewToolSpecs returned %d specs, want 3 (create_workflow, create_nodes, list_workflows): %+v", len(got), got)
	}
	if byName["create_workflow"].Description != "canvas version" {
		t.Fatalf("create_workflow should keep the first (canvas) definition, got: %+v", byName["create_workflow"])
	}
	if _, ok := byName["list_workflows"]; !ok {
		t.Fatal("expected list_workflows (a genuinely new name from adding) to be present")
	}
}
