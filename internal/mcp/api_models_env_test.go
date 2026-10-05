package mcp

// The documents of api_status and api_config_get describe this server's environment, which is
// Options.APIEnv's when a host or a test gives one and the process's own otherwise. api_models_list
// described the process's own whatever it was given (a nit of the correctness review of phase 6, with
// no effect in production, where the two are one): it reads the server's, as they do.

import (
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
)

func TestAPIModelsListReadsTheServersOwnEnvironment(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	s, _ := newAPIKeyServer(t, false)
	s.opts.APIEnv = apiconfig.Env{Getenv: func(name string) string {
		if name == "MONOAGENT_API_CONFINEMENT" {
			return "chat-only"
		}
		return ""
	}}
	if r := modelsReport(t, s, nil); r.Policy.Confinement != "chat-only" {
		t.Errorf("the confinement of this server's environment was not read: %+v", r.Policy)
	}
}
