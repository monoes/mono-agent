package orgdecide

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// PendingHIL lists Human-in-Loop items paused inside executions an org
// started — through a grant (org_tool) or a message to an automation role
// (org_message). They are that org's decisions, at the tier of the grant
// that started them (U12, Q12, class hil:<alias>). HIL items of standalone
// workflows are never touched.
func PendingHIL(ctx context.Context, db *sql.DB, profileID, org string, doc *orgdesign.Doc) ([]Item, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT h.id, COALESCE(h.node_name, ''), COALESCE(h.readonly_data, ''), COALESCE(e.trigger_data, '{}'), e.trigger_type, h.execution_id, COALESCE(h.created_at, '')
		FROM hil_pending h JOIN workflow_executions e ON e.id = h.execution_id
		WHERE h.status = 'pending' AND h.profile_id = ? AND e.trigger_type IN ('org_tool', 'org_message')`, profileID)
	if err != nil {
		return nil, fmt.Errorf("orgdecide: hil items: %w", err)
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var id, node, readonly, triggerData, triggerType, execID, created string
		if err := rows.Scan(&id, &node, &readonly, &triggerData, &triggerType, &execID, &created); err != nil {
			return nil, err
		}
		var td struct {
			Org struct {
				Name       string `json:"name"`
				Role       string `json:"role"`
				Automation string `json:"automation"`
			} `json:"org"`
			Message struct {
				Org  string `json:"org"`
				Role string `json:"role"`
				From string `json:"from"`
			} `json:"org_message"`
		}
		_ = json.Unmarshal([]byte(triggerData), &td)
		alias, requester := "", ""
		switch triggerType {
		case "org_tool":
			if td.Org.Name != org {
				continue
			}
			alias, requester = td.Org.Automation, td.Org.Role
		case "org_message":
			if td.Message.Org != org {
				continue
			}
			requester = td.Message.From
			if doc != nil {
				if r, _ := doc.FindRole(td.Message.Role); r != nil && r.Automation != nil {
					alias = r.Automation.Alias
				}
			}
		}
		if alias == "" {
			alias = "unknown"
		}
		ts := int64(0)
		if t, err := time.Parse("2006-01-02 15:04:05", created); err == nil {
			ts = t.UnixMilli()
		}
		items = append(items, Item{
			Kind: KindHIL, Ref: id, Requester: requester, Class: "hil:" + alias, Name: node,
			Text:      fmt.Sprintf("Paused at node %q of execution %s. Data for review:\n%s", node, execID, readonly),
			Summary:   fmt.Sprintf("automation %s paused at %q for review", alias, node),
			WaitingMS: ts, Hash: hashItem(KindHIL, requester, alias, node, readonly),
		})
	}
	return items, rows.Err()
}

// resolveHIL approves or rejects a HIL item; the paused run resumes on the
// engine's next poll.
func resolveHIL(ctx context.Context, db *sql.DB, id string, approve bool) error {
	status := "rejected"
	if approve {
		status = "approved"
	}
	res, err := db.ExecContext(ctx, `UPDATE hil_pending SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = 'pending'`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("HIL item %s is no longer pending", id)
	}
	return nil
}
