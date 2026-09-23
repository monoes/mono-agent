package org

import (
	"context"
	"errors"
	"fmt"

	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/workflow"
)

// OrgSendNode sends a message to a role of an org and continues without
// waiting for an answer.
//
// Config fields:
//
//	"org_name" (string, required)
//	"role"     (string): target role; empty = the org's coordinator.
//	"subject"  (string): supports {{$json.FIELD}}.
//	"message"  (string, required): supports {{$json.FIELD}}.
//	"output_key" (string): where the receipt goes (default "org_send").
//
// The sender is the workflow's automation role when the workflow is one in
// that org, otherwise "workflow:<execution>". A running org receives the
// message live (with monomind capability org-tool-providers); a stopped
// org queues it for its next start, and the receipt says which.
type OrgSendNode struct{}

func (n *OrgSendNode) Type() string { return "org.send" }

func (n *OrgSendNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	orgName := configString(config, "org_name", "")
	if !orgdesign.ValidOrgName(orgName) {
		return nil, fmt.Errorf("%w: org.send node requires a valid \"org_name\"", workflow.ErrInvalidConfig)
	}
	msgTemplate := configString(config, "message", "")
	if msgTemplate == "" {
		return nil, fmt.Errorf("%w: org.send node requires \"message\"", workflow.ErrInvalidConfig)
	}
	env, err := resolveOrgEnv(ctx, "org.send")
	if err != nil {
		return nil, err
	}
	item := firstItem(input.Items)
	res, err := orgbridge.Send(ctx, orgbridge.NewLedger(env.db), orgbridge.SendRequest{
		ProfileID:   env.profileID,
		Root:        env.root,
		Org:         orgName,
		To:          configString(config, "role", ""),
		From:        senderFor(env.root, orgName, input),
		Subject:     expandTemplate(configString(config, "subject", ""), item),
		Body:        expandTemplate(msgTemplate, item),
		Trace:       incomingTrace(ctx, item),
		WorkflowID:  input.WorkflowID,
		ExecutionID: input.ExecutionID,
		Limits:      orgLimits(env.root, orgName),
	})
	if err != nil {
		var refused *orgbridge.ErrRefused
		if errors.As(err, &refused) {
			return nil, fmt.Errorf("org.send (%s): refused by loop control: %s", orgName, refused.Reason)
		}
		return nil, fmt.Errorf("org.send (%s): %w", orgName, err)
	}
	out := copyItemJSON(item)
	out[configString(config, "output_key", "org_send")] = map[string]interface{}{
		"org":        res.Org,
		"to":         res.To,
		"from":       res.From,
		"delivery":   res.Delivery,
		"receipt":    res.Receipt,
		"message_id": res.MessageID,
	}
	out["trace"] = map[string]interface{}{"chain_id": res.Trace.ChainID, "hop": res.Trace.Hop}
	return []workflow.NodeOutput{{Handle: "main", Items: []workflow.Item{{JSON: out}}}}, nil
}
