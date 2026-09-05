// Package discoverynodes exposes internal/discovery as a workflow node
// type: discovery.search_jobs.
package discoverynodes

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/discoveryregistry"
	"github.com/monoes/mono-agent/internal/workflow"
)

// globalStore is the process-wide applications.Store used by this node.
var globalStore *applications.Store

// SetGlobalStore wires the shared SQLite connection into this package's
// node(s).
func SetGlobalStore(db *sql.DB) {
	globalStore = applications.NewStore(db)
}

// RegisterAll registers discovery.search_jobs into the registry.
func RegisterAll(r *workflow.NodeTypeRegistry, db *sql.DB) {
	SetGlobalStore(db)
	r.Register("discovery.search_jobs", func() workflow.NodeExecutor { return &SearchJobsNode{} })
}

func configString(config map[string]interface{}, key, def string) string {
	if v, ok := config[key].(string); ok && v != "" {
		return v
	}
	return def
}

// SearchJobsNode searches one discovery.Source and imports non-duplicate
// results as new pending job applications.
// Type: "discovery.search_jobs"
type SearchJobsNode struct{}

func (n *SearchJobsNode) Type() string { return "discovery.search_jobs" }

func (n *SearchJobsNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	if globalStore == nil {
		return nil, fmt.Errorf("discovery.search_jobs: store not available (call SetGlobalStore at startup)")
	}
	keywords := configString(config, "keywords", "")
	if keywords == "" {
		return nil, fmt.Errorf("discovery.search_jobs: config \"keywords\" is required")
	}
	sourceName := configString(config, "source", "linkedin")
	source, ok := discoveryregistry.Get(sourceName)
	if !ok {
		return nil, fmt.Errorf("discovery.search_jobs: unknown source %q (available: %v)", sourceName, discoveryregistry.Names())
	}
	profileID := configString(config, "profile_id", "default")
	location := configString(config, "location", "")
	limit := 25
	if v, ok := config["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	created, _, _, err := discovery.Search(ctx, source, globalStore, profileID, discovery.SearchQuery{
		Keywords: keywords, Location: location, Limit: limit,
	})
	if err != nil && len(created) == 0 {
		return nil, fmt.Errorf("discovery.search_jobs: %w", err)
	}

	items := make([]workflow.Item, 0, len(created))
	for _, app := range created {
		items = append(items, workflow.NewItem(map[string]interface{}{
			"id": app.ID, "kind": string(app.Kind), "status": string(app.Status),
			"title": app.Job.Title, "company": app.Job.Company, "url": app.Job.URL,
		}))
	}
	return []workflow.NodeOutput{{Handle: "main", Items: items}}, nil
}
