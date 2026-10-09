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

// servedTools is what this server serves: every tool, or with Options.TasksOnly the task board's alone,
// or with Options.APIOnly the API's alone. A model that is to work the board or manage the API through a
// server does not need a workflow, vault, secret, person, org or documentation tool, and
// --allow-mutations, which the mutating tools of both families need, also serves workflow tools that can
// run a command as the OS user: this is how an operator gives it the one without the other. Serve
// refuses a server asked for both.
func (s *Server) servedTools() []tool {
	all := allTools()
	var keep map[string]bool
	switch {
	case s.opts.TasksOnly:
		keep = taskToolNames()
		// task_approve is served only while the operator allows it for the profile (read now).
		if s.approveDelegated() {
			return append(narrow(all, keep), taskApproveTool())
		}
	case s.opts.APIOnly:
		keep = apiToolNames()
	default:
		return all
	}
	return narrow(all, keep)
}

// narrow keeps the tools of all whose names are in keep.
func narrow(all []tool, keep map[string]bool) []tool {
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

// notServed is the answer to a call, by name, of a tool that exists and that a
// narrowed server does not serve: it names the switch that narrowed it.
func (s *Server) notServed(name string) error {
	if s.opts.TasksOnly {
		return notServedByTasksOnly(name)
	}
	return notServedByAPIOnly(name)
}
