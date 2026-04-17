//go:build !cgo

package image

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/workflow"
)

// Execute returns a clear error when the binary was compiled without CGo.
// Rebuild with CGO_ENABLED=1 to enable U2-Net background removal.
func (n *RemoveBackgroundNode) Execute(_ context.Context, _ workflow.NodeInput, _ map[string]interface{}) ([]workflow.NodeOutput, error) {
	return nil, fmt.Errorf(
		"image.remove_background requires CGo (ONNX Runtime bindings).\n" +
			"Rebuild the CLI with CGo enabled:\n" +
			"  go install github.com/monoes/mono-agent/cmd/monoes@latest\n" +
			"or:\n" +
			"  CGO_ENABLED=1 go build -o monoagent ./cmd/monoes",
	)
}
