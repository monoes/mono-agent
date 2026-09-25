package health

import (
	"context"
	"fmt"
)

// TypeSafe Jev (optional decision provider): is a key configured, and —
// on demand, over the network — does the API accept it.
const (
	CheckJevKey = "jev.key"
	CheckJevAPI = "jev.api"

	FixJevKey = "jev.key.add"
)

func jevChecks() []Check {
	return []Check{
		{ID: CheckJevKey, Group: GroupIntegrations, Title: "TypeSafe Jev API key", Features: []string{"jev"}, Run: checkJevKey},
		{ID: CheckJevAPI, Group: GroupIntegrations, Title: "TypeSafe Jev API", Features: []string{"jev"},
			Network: true, OnDemand: true, Run: checkJevAPI},
	}
}

func jevFixes() []Fix {
	return []Fix{
		{FixInfo: FixInfo{ID: FixJevKey, Label: "Store a TypeSafe API key in the vault (or set TYPESAFE_API_KEY)", Safety: SafetyManual,
			Command: "monoagentcli secret add --kind secret --name typesafe"},
			Apply: func(context.Context, *Env, func(string)) error { return fmt.Errorf("this needs to be done by hand") }},
	}
}

func checkJevKey(ctx context.Context, env *Env) Result {
	if env.JevKey == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	source, err := env.JevKey(ctx)
	if err != nil || source == "" {
		return Result{Status: StatusWarn, Summary: "no key (Jev is optional; its surfaces stay off without one)", FixID: FixJevKey}
	}
	return Result{Status: StatusOK, Summary: "key from " + source}
}

func checkJevAPI(ctx context.Context, env *Env) Result {
	if env.JevModels == nil || env.JevKey == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	if source, err := env.JevKey(ctx); err != nil || source == "" {
		return Result{Status: StatusSkip, Summary: "no key"}
	}
	if err := env.JevModels(ctx); err != nil {
		return Result{Status: StatusWarn, Summary: err.Error()}
	}
	return Result{Status: StatusOK, Summary: "API reachable, key accepted"}
}
