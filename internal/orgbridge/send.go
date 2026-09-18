package orgbridge

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/monomind"
)

// inboxFunc delivers a message; tests replace it.
var inboxFunc = monomind.OrgInbox

// SendRequest is one message mono-agent sends into an org.
type SendRequest struct {
	ProfileID   string
	Root        string // profile root the org lives under
	Org         string
	To          string // role ("" = the org's coordinator)
	From        string // qualified sender, e.g. "workflow:ab12cd34" or "growth:publisher-bot"
	Subject     string
	Body        string
	Trace       Trace // incoming chain position ("" chain starts one)
	OriginOrg   string
	Direction   string // DirWorkflowOut by default
	WorkflowID  string
	ExecutionID string
	Limits      Limits
}

// SendResult is the receipt plus the chain position the message carries.
type SendResult struct {
	monomind.InboxReceipt
	Trace Trace `json:"trace"`
}

// ErrRefused is returned when loop control refuses a crossing.
type ErrRefused struct {
	Status string
	Reason string
}

func (e *ErrRefused) Error() string { return e.Status + ": " + e.Reason }

// Send admits the crossing through the ledger (hop and repeat limits),
// writes the trace header as the body's first line, and delivers through
// `monomind org inbox`. The receipt reports live vs queued exactly as
// monomind does; a queued message waits for the org's next start (C-35).
func Send(ctx context.Context, ledger *Ledger, req SendRequest) (*SendResult, error) {
	if req.Org == "" || req.From == "" {
		return nil, fmt.Errorf("orgbridge: send needs an org and a sender")
	}
	dir := req.Direction
	if dir == "" {
		dir = DirWorkflowOut
	}
	adm, err := ledger.Admit(ctx, Call{
		ProfileID: req.ProfileID, Trace: req.Trace, OriginOrg: req.OriginOrg, Direction: dir,
		OrgName: req.Org, RoleID: req.To, WorkflowID: req.WorkflowID, ExecutionID: req.ExecutionID,
	}, req.Limits)
	if err != nil {
		return nil, err
	}
	if !adm.OK() {
		return nil, &ErrRefused{Status: adm.Status, Reason: adm.Reason}
	}
	rc, err := inboxFunc(ctx, req.Root, req.Org, monomind.InboxMessage{
		To: req.To, From: req.From, Subject: req.Subject, Body: WithTrace(req.Body, adm.Trace),
	})
	if err != nil {
		_ = ledger.SetStatus(ctx, adm.ID, StatusError)
		return nil, err
	}
	return &SendResult{InboxReceipt: *rc, Trace: adm.Trace}, nil
}
