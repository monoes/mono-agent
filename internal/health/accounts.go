package health

import (
	"context"
	"fmt"
	"time"
)

// GroupAccounts covers the credentials the active profile has saved.
const GroupAccounts = "accounts"

const (
	CheckConnections = "accounts.connections"
	CheckAIProviders = "accounts.ai_connections"
	CheckLogins      = "accounts.logins"

	// FixConnectionRefresh is parameterized by connection id.
	FixConnectionRefresh = "accounts.connection.refresh"
	FixReconnect         = "accounts.connection.reconnect"
	FixAIProviderKey     = "accounts.ai_connection.edit"
	FixLogin             = "accounts.login"
)

const accountTimeout = 2 * time.Minute

func accountChecks() []Check {
	return []Check{
		{ID: CheckConnections, Group: GroupAccounts, Title: "Connections", Features: []string{"workflow nodes using them"},
			DependsOn: []string{CheckProfile}, Network: true, Timeout: accountTimeout, Run: checkConnections},
		{ID: CheckAIProviders, Group: GroupAccounts, Title: "AI connections (legacy)", Features: []string{"AI nodes using them"},
			DependsOn: []string{CheckProfile}, Network: true, Timeout: accountTimeout, Run: checkAIProviders},
		{ID: CheckLogins, Group: GroupAccounts, Title: "Platform logins", Features: []string{"social platform actions"},
			DependsOn: []string{CheckProfile}, Run: checkLogins},
	}
}

func accountFixes() []Fix {
	manual := func(context.Context, *Env, func(string)) error { return fmt.Errorf("this needs to be done by hand") }
	return []Fix{
		{FixInfo: FixInfo{ID: FixConnectionRefresh, Label: "Refresh the connection's token", Safety: SafetyAuto,
			Command: "monoagentcli connect refresh {arg}"},
			ApplyArg: func(ctx context.Context, env *Env, id string, progress func(string)) error {
				if env.RefreshConnection == nil {
					return fmt.Errorf("not available here")
				}
				progress("exchanging the stored refresh token for connection " + id)
				return env.RefreshConnection(ctx, id)
			}},
		{FixInfo: FixInfo{ID: FixReconnect, Label: "Reconnect the account", Safety: SafetyManual,
			Command: "monoagentcli connect <platform> (or Connections in the app)"}, Apply: manual},
		{FixInfo: FixInfo{ID: FixAIProviderKey, Label: "Update the API key", Safety: SafetyManual,
			Command: "Settings › AI connections (legacy), or: monoagentcli ai provider add"}, Apply: manual},
		{FixInfo: FixInfo{ID: FixLogin, Label: "Log in again", Safety: SafetyManual,
			Command: "monoagentcli login <platform>"}, Apply: manual},
	}
}

func checkConnections(ctx context.Context, env *Env) Result {
	if env.Connections == nil || env.TestConnection == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	conns, err := env.Connections(ctx)
	if err != nil {
		return Result{Status: StatusFail, Summary: "cannot list connections", Detail: err.Error()}
	}
	if len(conns) == 0 {
		return Result{Status: StatusInfo, Summary: "none saved"}
	}
	res, failing := Result{}, 0
	for _, c := range conns {
		child := Result{ID: "accounts.connection." + shortID(c.ID), Title: orDefault(c.Platform, "(no platform)"),
			Summary: orDefault(c.Label, "connection "+shortID(c.ID))}
		if err := env.TestConnection(ctx, c.ID); err != nil {
			failing++
			child.Status = StatusFail
			child.Detail = err.Error()
			child.FixID = FixReconnect
			if c.Method == "oauth" && c.HasRefreshToken {
				child.FixID = FixConnectionRefresh + ":" + c.ID
			}
		} else {
			child.Status = StatusOK
		}
		res.Children = append(res.Children, child)
	}
	if failing > 0 {
		res.Status, res.Summary = StatusWarn, fmt.Sprintf("%d of %d failing", failing, len(conns))
	} else {
		res.Status, res.Summary = StatusOK, fmt.Sprintf("all %d working", len(conns))
	}
	return res
}

func checkAIProviders(ctx context.Context, env *Env) Result {
	if env.AIProviders == nil || env.TestAIProvider == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	providers, err := env.AIProviders(ctx)
	if err != nil {
		return Result{Status: StatusFail, Summary: "cannot list AI connections", Detail: err.Error()}
	}
	if len(providers) == 0 {
		return Result{Status: StatusInfo, Summary: "none configured"}
	}
	res, failing := Result{}, 0
	for _, p := range providers {
		child := Result{ID: "accounts.ai_connection." + shortID(p.ID), Title: p.Name,
			Summary: fmt.Sprintf("%s · %s", p.ProviderID, p.Model)}
		if err := env.TestAIProvider(ctx, p.ID); err != nil {
			failing++
			child.Status, child.Detail, child.FixID = StatusFail, err.Error(), FixAIProviderKey
		} else {
			child.Status = StatusOK
		}
		res.Children = append(res.Children, child)
	}
	if failing > 0 {
		res.Status, res.Summary = StatusWarn, fmt.Sprintf("%d of %d failing", failing, len(providers))
	} else {
		res.Status, res.Summary = StatusOK, fmt.Sprintf("all %d answering", len(providers))
	}
	return res
}

func checkLogins(ctx context.Context, env *Env) Result {
	if env.LoginSessions == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	sessions, err := env.LoginSessions(ctx)
	if err != nil {
		return Result{Status: StatusSkip, Summary: "cannot read login sessions", Detail: err.Error()}
	}
	if len(sessions) == 0 {
		return Result{Status: StatusSkip, Summary: "no platform logins saved"}
	}
	res, expired := Result{}, 0
	now := time.Now()
	for i, s := range sessions {
		child := Result{ID: fmt.Sprintf("accounts.login.%d", i), Title: orDefault(s.Platform, "(no platform)")}
		if s.Expiry.Before(now) {
			expired++
			child.Status, child.FixID = StatusWarn, FixLogin
			child.Summary = fmt.Sprintf("%s — expired %s", s.Username, s.Expiry.Format("2006-01-02"))
		} else {
			child.Status = StatusOK
			child.Summary = fmt.Sprintf("%s — valid until %s", s.Username, s.Expiry.Format("2006-01-02"))
		}
		res.Children = append(res.Children, child)
	}
	if expired > 0 {
		res.Status, res.Summary = StatusWarn, fmt.Sprintf("%d of %d expired", expired, len(sessions))
	} else {
		res.Status, res.Summary = StatusOK, fmt.Sprintf("%d valid", len(sessions))
	}
	return res
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
