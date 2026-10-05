package mcp

import "testing"

// Whether api_config_set may widen what the API's server exposes is the operator's decision, made
// when the MCP server starts: by the flag, or by MONOAGENT_MCP_ALLOW_API_EXPOSURE=1 read the way
// MONOAGENT_MCP_ALLOW_MUTATIONS is. Nothing a model sends is part of it.
func TestAllowAPIExposureIsTheOperatorsAndOnlyThe1OfItsOwnVariable(t *testing.T) {
	for _, c := range []struct {
		name      string
		exposure  string // MONOAGENT_MCP_ALLOW_API_EXPOSURE
		mutations string // MONOAGENT_MCP_ALLOW_MUTATIONS
		option    bool
		want      bool
	}{
		{"nothing set", "", "", false, false},
		{"the variable is 1", "1", "", false, true},
		{"the flag", "", "", true, true},
		{"both", "1", "", true, true},
		{"a variable that is not 1", "true", "", false, false},
		{"0", "0", "", false, false},
		{"the mutations variable is not this one", "", "1", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MONOAGENT_MCP_ALLOW_API_EXPOSURE", c.exposure)
			t.Setenv("MONOAGENT_MCP_ALLOW_MUTATIONS", c.mutations)
			s := NewServer(Options{AllowAPIExposure: c.option})
			if s.opts.AllowAPIExposure != c.want {
				t.Errorf("AllowAPIExposure = %v, want %v", s.opts.AllowAPIExposure, c.want)
			}
			if (c.mutations == "1") != s.opts.AllowMutations {
				t.Errorf("AllowMutations = %v with MONOAGENT_MCP_ALLOW_MUTATIONS=%q: the two switches must not move each other", s.opts.AllowMutations, c.mutations)
			}
		})
	}
}
