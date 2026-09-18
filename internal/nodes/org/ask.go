package org

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/workflow"
)

// askPollWindow is how long an org.ask pause waits before the engine's
// resume poll checks it again. The bridge resumes it as soon as the reply
// arrives, so this is only a safety net (C-33).
const askPollWindow = 30 * time.Second

// OrgAskNode sends a question to an org role and pauses the execution until
// that role replies or the timeout passes.
//
// Config fields:
//
//	"org_name" (string, required)
//	"role"     (string): role to ask; empty = the org's coordinator.
//	"question" (string, required): supports {{$json.FIELD}}.
//	"timeout_seconds" (number): default 600.
//	"output_key" (string): default "org_reply".
//
// The workflow must be one of the org's automation roles: that role is the
// return address. The question goes out with subject "ask:<id> …"; the
// reply is matched by that id in its subject or body (contracts §9).
type OrgAskNode struct{}

func (n *OrgAskNode) Type() string { return "org.ask" }

func (n *OrgAskNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	orgName := configString(config, "org_name", "")
	if !orgdesign.ValidOrgName(orgName) {
		return nil, fmt.Errorf("%w: org.ask node requires a valid \"org_name\"", workflow.ErrInvalidConfig)
	}
	question := configString(config, "question", "")
	if question == "" {
		return nil, fmt.Errorf("%w: org.ask node requires \"question\"", workflow.ErrInvalidConfig)
	}
	timeout := time.Duration(configInt(config, "timeout_seconds", 600)) * time.Second
	if timeout <= 0 {
		timeout = 600 * time.Second
	}
	outputKey := configString(config, "output_key", "org_reply")

	env, err := resolveOrgEnv(ctx, "org.ask")
	if err != nil {
		return nil, err
	}
	asks := orgbridge.NewAskStore(env.db)
	ask, err := asks.ForNode(ctx, input.ExecutionID, input.NodeID)
	if err != nil {
		return nil, fmt.Errorf("org.ask (%s): %w", orgName, err)
	}
	item := firstItem(input.Items)

	if ask != nil {
		switch ask.Status {
		case orgbridge.AskReplied:
			out := copyItemJSON(item)
			out[outputKey] = ask.Reply
			return []workflow.NodeOutput{{Handle: "main", Items: []workflow.Item{{JSON: out}}}}, nil
		case orgbridge.AskTimedOut:
			return nil, fmt.Errorf("org.ask (%s): no reply within %s", orgName, ask.DeadlineAt.Sub(ask.CreatedAt).Round(time.Second))
		default:
			if time.Now().After(ask.DeadlineAt) {
				if _, err := asks.MarkTimedOut(ctx, ask.ID); err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("org.ask (%s): no reply within %s", orgName, ask.DeadlineAt.Sub(ask.CreatedAt).Round(time.Second))
			}
			return nil, workflow.PauseFor(minDuration(askPollWindow, time.Until(ask.DeadlineAt)), "waiting for a reply from "+orgName)
		}
	}

	endpoint := automationRoleFor(env.root, orgName, input.WorkflowID)
	if endpoint == nil {
		return nil, fmt.Errorf("%w: org.ask needs this workflow to be an automation role of org %q (add it with `monoagentcli org automation-role add %s`) so the reply has an address", workflow.ErrInvalidConfig, orgName, orgName)
	}
	id := orgbridge.NewAskID()
	body := expandTemplate(question, item)
	subject := "ask:" + id + " " + firstLine(body, 60)
	if _, err := orgbridge.Send(ctx, orgbridge.NewLedger(env.db), orgbridge.SendRequest{
		ProfileID:   env.profileID,
		Root:        env.root,
		Org:         orgName,
		To:          configString(config, "role", ""),
		From:        orgName + ":" + endpoint.ID,
		Subject:     subject,
		Body:        body + "\n\nReply with org_send to " + endpoint.ID + " and keep \"ask:" + id + "\" in the subject.",
		Trace:       incomingTrace(item),
		WorkflowID:  input.WorkflowID,
		ExecutionID: input.ExecutionID,
		Limits:      orgLimits(env.root, orgName),
	}); err != nil {
		var refused *orgbridge.ErrRefused
		if errors.As(err, &refused) {
			return nil, fmt.Errorf("org.ask (%s): refused by loop control: %s", orgName, refused.Reason)
		}
		return nil, fmt.Errorf("org.ask (%s): %w", orgName, err)
	}
	if err := asks.Create(ctx, orgbridge.Ask{
		ID: id, ProfileID: env.profileID, OrgName: orgName, RoleID: configString(config, "role", ""),
		EndpointRoleID: endpoint.ID, ExecutionID: input.ExecutionID, NodeID: input.NodeID,
		DeadlineAt: time.Now().Add(timeout),
	}); err != nil {
		return nil, fmt.Errorf("org.ask (%s): record ask: %w", orgName, err)
	}
	return nil, workflow.PauseFor(minDuration(askPollWindow, timeout), "waiting for a reply from "+orgName)
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func minDuration(a, b time.Duration) time.Duration {
	if b < a {
		if b < time.Second {
			return time.Second
		}
		return b
	}
	return a
}
