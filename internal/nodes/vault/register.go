package vault

import "github.com/monoes/mono-agent/internal/workflow"

// RegisterAll registers the credential vault nodes into the given registry.
func RegisterAll(r *workflow.NodeTypeRegistry) {
	r.Register("vault.secret_save", func() workflow.NodeExecutor { return &SecretSaveNode{} })
	r.Register("vault.secret_get", func() workflow.NodeExecutor { return &SecretGetNode{} })
}
