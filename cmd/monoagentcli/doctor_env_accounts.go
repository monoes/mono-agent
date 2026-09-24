package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/connections"
	"github.com/monoes/mono-agent/internal/health"
)

// addAccountHooks wires the accounts checks to the profile's saved
// credentials. Nothing secret leaves these hooks.
func addAccountHooks(env *health.Env, db *sql.DB) {
	profileID := env.ProfileID
	env.Connections = func(ctx context.Context) ([]health.ConnectionInfo, error) {
		mgr, err := connections.NewManager(db)
		if err != nil {
			return nil, err
		}
		conns, err := mgr.List(ctx, "", profileID)
		if err != nil {
			return nil, err
		}
		out := make([]health.ConnectionInfo, 0, len(conns))
		for _, c := range conns {
			rt, _ := c.Data["refresh_token"].(string)
			out = append(out, health.ConnectionInfo{ID: c.ID, Platform: c.Platform, Label: c.Label,
				Method: string(c.Method), HasRefreshToken: rt != ""})
		}
		return out, nil
	}
	env.TestConnection = func(ctx context.Context, id string) error {
		mgr, err := connections.NewManager(db)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return mgr.Test(ctx, id)
	}
	env.RefreshConnection = func(ctx context.Context, id string) error {
		mgr, err := connections.NewManager(db)
		if err != nil {
			return err
		}
		conn, err := mgr.Get(ctx, id)
		if err != nil || conn == nil {
			return fmt.Errorf("connection %q not found", id)
		}
		// Manager.Refresh falls back to the interactive browser flow when
		// there is no refresh token; a fix must never do that.
		if rt, _ := conn.Data["refresh_token"].(string); conn.Method != connections.MethodOAuth || rt == "" {
			return fmt.Errorf("connection %q has no refresh token — reconnect it: monoagentcli connect %s", id, conn.Platform)
		}
		return mgr.Refresh(ctx, id, time.Minute)
	}

	env.AIProviders = func(context.Context) ([]health.ProviderInfo, error) {
		store, err := ai.NewAIStore(db)
		if err != nil {
			return nil, err
		}
		ps, err := store.ListProviders(profileID)
		if err != nil {
			return nil, err
		}
		out := make([]health.ProviderInfo, 0, len(ps))
		for _, p := range ps {
			out = append(out, health.ProviderInfo{ID: p.ID, Name: p.Name, ProviderID: p.ProviderID, Model: p.DefaultModel})
		}
		return out, nil
	}
	env.TestAIProvider = func(ctx context.Context, id string) error {
		store, err := ai.NewAIStore(db)
		if err != nil {
			return err
		}
		p, err := store.GetProvider(id, profileID)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, err = testAIProvider(ctx, store, p, profileID)
		return err
	}

	env.LoginSessions = func(ctx context.Context) ([]health.SessionInfo, error) {
		rows, err := db.QueryContext(ctx,
			`SELECT platform, username, expiry FROM crawler_sessions WHERE profile_id = ? ORDER BY platform`, profileID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []health.SessionInfo
		for rows.Next() {
			var s health.SessionInfo
			if err := rows.Scan(&s.Platform, &s.Username, &s.Expiry); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, rows.Err()
	}
}
