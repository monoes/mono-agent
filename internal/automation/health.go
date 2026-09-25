package automation

import (
	"database/sql"

	"github.com/monoes/mono-agent/internal/action"
)

// HealthObserver returns the selector-health observer backed by the
// automation_selector_health table (spec §8.7). Owned by the health builder.
func HealthObserver(db *sql.DB) action.SelectorObserver { return nil }
