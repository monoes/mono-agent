package mcp

import "fmt"

// apiToolNames is the tools of the OpenAI-compatible API, by name: the keys, the models, the status, the
// settings and the auto model (the families apiTools and apiConfigTools, the second of which has api_auto_set).
// It is what a server started with --api-only serves, and it is the family of the names that start with api_:
// a test keeps the two in step.
func apiToolNames() map[string]bool {
	names := map[string]bool{}
	for _, t := range apiTools() {
		names[t.name] = true
	}
	for _, t := range apiConfigTools() {
		names[t.name] = true
	}
	return names
}

// servedTools is what this server serves: every tool, or with Options.APIOnly the API's alone. A model
// that is to manage the API through a server does not need a workflow, vault, secret, person, org or
// documentation tool, and --allow-mutations, which the API's mutating tools need, also serves workflow
// tools that can run a command as the OS user: this is how an operator gives it the one without the other.
func (s *Server) servedTools() []tool {
	all := allTools()
	if !s.opts.APIOnly {
		return all
	}
	keep := apiToolNames()
	out := make([]tool, 0, len(keep))
	for _, t := range all {
		if keep[t.name] {
			out = append(out, t)
		}
	}
	return out
}

// notServedByAPIOnly is the answer to a call, by name, of a tool that exists and that this server does not
// serve because it was started with --api-only.
func notServedByAPIOnly(name string) error {
	return fmt.Errorf("%s is not served: this MCP server was started with --api-only (or MONOAGENT_MCP_API_ONLY=1), which serves only the OpenAI-compatible API's tools (api_*)", name)
}
