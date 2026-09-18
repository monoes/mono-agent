package orgbridge

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/workflow"
)

// TriggerNodeType is the workflow node that starts a run from an org.
const TriggerNodeType = "trigger.org"

// maxEventContentBytes caps an asset event's file content copied into
// trigger data — the same 16 KB bound grant tool outputs get (C-47).
const maxEventContentBytes = 16 * 1024

// EventFilter is trigger.org's event-mode configuration.
type EventFilter struct {
	Org          string
	Role         string   // matches the event's from or to (bare or org-qualified)
	EventTypes   []string // default: every type
	SubjectMatch string   // case-insensitive substring of subject (messages) or gate name
	QuestionKind string   // ask_human (default) | approval | any
}

// FilterFromConfig reads trigger.org node config.
func FilterFromConfig(cfg map[string]interface{}) EventFilter {
	f := EventFilter{
		Org:          strings.TrimSpace(str(cfg["org_name"])),
		Role:         strings.TrimSpace(str(cfg["role"])),
		SubjectMatch: strings.TrimSpace(str(cfg["subject_match"])),
		QuestionKind: strings.TrimSpace(str(cfg["question_kind"])),
	}
	switch v := cfg["event_types"].(type) {
	case []interface{}:
		for _, x := range v {
			if s := strings.TrimSpace(str(x)); s != "" {
				f.EventTypes = append(f.EventTypes, s)
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				f.EventTypes = append(f.EventTypes, s)
			}
		}
	}
	if f.QuestionKind == "" {
		f.QuestionKind = "ask_human"
	}
	return f
}

func str(v interface{}) string {
	s, _ := v.(string)
	return s
}

// Match reports whether ev passes the filter.
func (f EventFilter) Match(ev Event) bool {
	if len(f.EventTypes) > 0 {
		ok := false
		for _, t := range f.EventTypes {
			if t == ev.Type {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if ev.Type == "question" && f.QuestionKind != "any" && ev.QuestionKind() != f.QuestionKind {
		return false
	}
	if f.Role != "" && !roleMatches(f.Role, ev.Org, ev.From) && !roleMatches(f.Role, ev.Org, ev.To) {
		return false
	}
	if f.SubjectMatch != "" {
		hay := ev.Subject
		if ev.Type == "gate" {
			hay = ev.DataString("name")
		}
		if !strings.Contains(strings.ToLower(hay), strings.ToLower(f.SubjectMatch)) {
			return false
		}
	}
	return true
}

func roleMatches(want, org, addr string) bool {
	if addr == "" {
		return false
	}
	return addr == want || addr == org+":"+want
}

// TriggerItems renders an event as trigger data: the event without an
// asset's full content (capped at 16 KB) and with credential-shaped keys
// redacted, plus the trace when the message carried one. Redaction only —
// the 4 KB display truncation would replace a long message with a stub.
func TriggerItems(ev Event) []workflow.Item {
	evJSON := map[string]interface{}{
		"id": ev.ID, "ts": ev.TS, "org": ev.Org, "run": ev.Run, "type": ev.Type,
	}
	for k, v := range map[string]string{"from": ev.From, "to": ev.To, "subject": ev.Subject, "msg": StripTrace(ev.Msg), "tool": ev.Tool, "reason": ev.Reason, "path": ev.Path} {
		if v != "" {
			evJSON[k] = v
		}
	}
	if len(ev.Data) > 0 {
		data := make(map[string]interface{}, len(ev.Data))
		for k, v := range ev.Data {
			data[k] = v
		}
		if c, ok := data["content"].(string); ok && len(c) > maxEventContentBytes {
			data["content"] = c[:maxEventContentBytes]
			data["content_truncated"] = true
		}
		evJSON["data"] = data
	}
	item := map[string]interface{}{
		"trigger_type": workflow.TriggerTypeOrgEvent,
		"org":          ev.Org,
		"event":        evJSON,
	}
	if ev.Type == "question" {
		item["question_kind"] = ev.QuestionKind()
	}
	if tr, ok := ParseTrace(ev.Msg); ok {
		item["trace"] = map[string]interface{}{"chain_id": tr.ChainID, "hop": tr.Hop}
	}
	return []workflow.Item{workflow.RedactItemJSON(workflow.Item{JSON: item})}
}

// TriggerSource implements workflow.TriggerSource for trigger.org. Event
// mode subscribes to the org's shared tail; endpoint mode registers the
// node so the automation-role receiver can fire it (Phase 4).
type TriggerSource struct {
	Mux *Mux
	DB  *sql.DB

	mu        sync.Mutex
	endpoints map[string]func(items []workflow.Item) // workflowID -> fire
}

// NewTriggerSource returns a trigger.org source.
func NewTriggerSource(mux *Mux, db *sql.DB) *TriggerSource {
	return &TriggerSource{Mux: mux, DB: db, endpoints: map[string]func([]workflow.Item){}}
}

// Activate implements workflow.TriggerSource.
func (s *TriggerSource) Activate(w *workflow.Workflow, node *workflow.WorkflowNode, fire func(items []workflow.Item)) (func(), error) {
	mode := strings.TrimSpace(str(node.Config["mode"]))
	if mode == "" {
		mode = "event"
		if str(node.Config["org_name"]) == "" {
			mode = "endpoint"
		}
	}
	switch mode {
	case "endpoint":
		s.mu.Lock()
		s.endpoints[w.ID] = fire
		s.mu.Unlock()
		return func() {
			s.mu.Lock()
			delete(s.endpoints, w.ID)
			s.mu.Unlock()
		}, nil
	case "event":
		f := FilterFromConfig(node.Config)
		if !orgdesign.ValidOrgName(f.Org) {
			return nil, fmt.Errorf("trigger.org: event mode needs a valid org_name")
		}
		profile := w.ProfileID
		if profile == "" {
			profile = "default"
		}
		root := profiledir.Root(s.DB, profile)
		unsub := s.Mux.Subscribe(root, f.Org, func(ev Event) {
			if !f.Match(ev) {
				return
			}
			fire(TriggerItems(ev))
		})
		return unsub, nil
	default:
		return nil, fmt.Errorf("trigger.org: mode %q must be event or endpoint", mode)
	}
}

// FireEndpoint starts the workflow's endpoint-mode trigger.org with items.
// It reports false when the workflow has no active endpoint trigger.
func (s *TriggerSource) FireEndpoint(workflowID string, items []workflow.Item) bool {
	s.mu.Lock()
	fire := s.endpoints[workflowID]
	s.mu.Unlock()
	if fire == nil {
		return false
	}
	fire(items)
	return true
}
