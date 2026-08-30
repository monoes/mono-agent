package main

import "testing"

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
