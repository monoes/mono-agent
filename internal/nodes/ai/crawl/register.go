package crawl

import (
	cfgpkg "github.com/monoes/mono-agent/internal/config"
	"github.com/monoes/mono-agent/internal/workflow"
)

func RegisterAll(r *workflow.NodeTypeRegistry, cfgClient *cfgpkg.APIClient) {
	r.Register("ai.read_page", func() workflow.NodeExecutor {
		return &ReadPageNode{}
	})
	r.Register("ai.extract_page", func() workflow.NodeExecutor {
		return &ExtractPageNode{CfgClient: cfgClient}
	})
}
