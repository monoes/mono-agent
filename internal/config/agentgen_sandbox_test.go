package config

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
)

// Config generation keeps its fresh temp folder and adds the sandbox only
// when monomind advertises it.
func TestGenerateConfigSandbox(t *testing.T) {
	t.Setenv(RuntimeEnvVar, "codex")
	for _, advertise := range []bool{true, false} {
		argsLog := sandboxtest.Install(t, advertise, `{"config_name":"gen","fields":{"title":{"xpath":"//title","type":"text","data":null}}}`)
		if _, err := NewAgentGenerator(zerolog.Nop()).GenerateConfig(context.Background(), "c", "<html></html>", "title", nil); err != nil {
			t.Fatalf("advertise=%v: %v", advertise, err)
		}
		sandboxtest.Check(t, argsLog, advertise, "")
	}
}
