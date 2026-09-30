package config

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
)

// Config generation keeps its fresh temp folder and adds whichever
// sandbox path monomind offers.
func TestGenerateConfigSandbox(t *testing.T) {
	t.Setenv(RuntimeEnvVar, "codex")
	for _, m := range sandboxtest.Kinds {
		argsLog := sandboxtest.Install(t, m, `{"config_name":"gen","fields":{"title":{"xpath":"//title","type":"text","data":null}}}`)
		if _, err := NewAgentGenerator(zerolog.Nop()).GenerateConfig(context.Background(), "c", "<html></html>", "title", nil); err != nil {
			t.Fatalf("%s monomind: %v", m, err)
		}
		sandboxtest.Check(t, argsLog, m, "")
	}
}
