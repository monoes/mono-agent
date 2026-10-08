package main

// Document channel of a sections org (monomind 2.24): published documents,
// decisions, rework rounds and the rework cap, for the Documents panel.
// Same doctrine as app_org_queued.go: the view is built by
// `monoagentcli org documents`, which reads monomind's per-run store
// read-only. No internal/monomind or internal/orgbridge import here.

func documentsArgs(org, run string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	if run == "" {
		return []string{"documents", org}, nil
	}
	return []string{"documents", org, "--run", run}, nil
}

// GetOrgDocuments returns `org documents <org> [--run <run>]`'s JSON (the
// orgbridge.DocView) or {"error"}. run "" is the newest run.
func (a *App) GetOrgDocuments(org, run string) string {
	sub, err := documentsArgs(org, run)
	return a.runOrgSub("documents", sub, err)
}
