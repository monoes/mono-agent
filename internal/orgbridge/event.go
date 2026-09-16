// Package orgbridge connects running monomind orgs to the workflow engine
// (docs/plans/2026-09-15-org-workflow-unification.md §7.2, §7.3): the trace
// header and loop limits every crossing carries (U10), one shared bus-event
// tail per org (C-27), message sending with honest live/queued receipts
// (C-21), trigger.org subscriptions, and the automation-role endpoint
// receiver.
package orgbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Event is one monomind bus event (`org events --follow` NDJSON line).
type Event struct {
	ID      string                 `json:"id"`
	TS      int64                  `json:"ts"`
	Org     string                 `json:"org"`
	Run     string                 `json:"run"`
	Type    string                 `json:"type"`
	From    string                 `json:"from,omitempty"`
	To      string                 `json:"to,omitempty"`
	Subject string                 `json:"subject,omitempty"`
	Msg     string                 `json:"msg,omitempty"`
	Tool    string                 `json:"tool,omitempty"`
	Reason  string                 `json:"reason,omitempty"`
	Path    string                 `json:"path,omitempty"`
	Data    map[string]interface{} `json:"data,omitempty"`

	Raw json.RawMessage `json:"-"`
}

// ParseEvent decodes one NDJSON line.
func ParseEvent(line []byte) (Event, error) {
	var ev Event
	if err := json.Unmarshal(line, &ev); err != nil {
		return ev, err
	}
	ev.Raw = append(json.RawMessage(nil), line...)
	return ev, nil
}

// DataString returns ev.Data[key] as a string ("" when absent).
func (ev Event) DataString(key string) string {
	s, _ := ev.Data[key].(string)
	return s
}

// MessageID is the message id monomind M3 stamps on every copy of a
// message ("" on older monomind).
func (ev Event) MessageID() string { return ev.DataString("messageId") }

// QuestionKind tells approval requests from ask_human questions: both are
// "question" events; approvals carry data.action, ask_human data.questionId
// (C-45).
func (ev Event) QuestionKind() string {
	if ev.Type != "question" {
		return ""
	}
	if ev.DataString("questionId") != "" {
		return "ask_human"
	}
	if ev.DataString("action") != "" {
		return "approval"
	}
	return ""
}

// dedupeKey identifies one logical cross-org message across the sender's and
// receiver's bus copies (C-43): the M3 messageId when present, otherwise a
// hash of sender, recipient, subject, and body.
func (ev Event) dedupeKey() string {
	if id := ev.MessageID(); id != "" {
		return "id:" + id
	}
	h := sha256.New()
	for _, s := range []string{ev.From, ev.To, ev.Subject, ev.Msg} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return "h:" + hex.EncodeToString(h.Sum(nil))
}
