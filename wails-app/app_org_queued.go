package main

// Queued inbox messages (plan 2026-09-15 C-35). monomind drains an org's
// offline queue only when the org starts; the org view lists what is waiting
// and offers "Start org now" through the existing RunOrg path.
//
// Same doctrine as app_org_unification.go: shells `monoagentcli org queued`,
// which reads monomind's inbox.jsonl read-only. No internal/monomind import.

func queuedArgs(org string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	return []string{"queued", org}, nil
}

// ListOrgQueuedMessages returns `org queued <org>`'s JSON:
// {"v":1,"org","count","skipped","delivery","messages":[…]} or {"error"}.
func (a *App) ListOrgQueuedMessages(org string) string {
	sub, err := queuedArgs(org)
	return a.runOrgSub("queued", sub, err)
}
