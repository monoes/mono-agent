// internal/nodes/applications/applications.go

// Package applicationsnodes exposes internal/applications as workflow node
// types: applications.create, applications.set_status, applications.tag,
// applications.list.
package applicationsnodes

import (
	"database/sql"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/workflow"
)

// globalStore is the process-wide applications.Store used by every node in
// this package. Set once at startup via RegisterAll.
var globalStore *applications.Store

// SetGlobalStore wires the shared SQLite connection into all nodes in this
// package.
func SetGlobalStore(db *sql.DB) {
	globalStore = applications.NewStore(db)
}

// RegisterAll registers all applications node types into the registry.
func RegisterAll(r *workflow.NodeTypeRegistry, db *sql.DB) {
	SetGlobalStore(db)
	r.Register("applications.create", func() workflow.NodeExecutor { return &CreateNode{} })
	r.Register("applications.set_status", func() workflow.NodeExecutor { return &SetStatusNode{} })
	r.Register("applications.tag", func() workflow.NodeExecutor { return &TagNode{} })
	r.Register("applications.list", func() workflow.NodeExecutor { return &ListNode{} })
}

// configString reads a string config key, returning def if absent or the
// wrong type.
func configString(config map[string]interface{}, key, def string) string {
	if v, ok := config[key].(string); ok && v != "" {
		return v
	}
	return def
}

// configFloatPtr reads a numeric config key (JSON numbers decode as
// float64), returning nil if absent.
func configFloatPtr(config map[string]interface{}, key string) *float64 {
	if v, ok := config[key].(float64); ok {
		return &v
	}
	return nil
}

// configBoolPtr reads a boolean config key, returning nil if absent.
func configBoolPtr(config map[string]interface{}, key string) *bool {
	if v, ok := config[key].(bool); ok {
		return &v
	}
	return nil
}
