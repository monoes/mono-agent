package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConnectionsCheck(t *testing.T) {
	env := &Env{
		Connections: func(context.Context) ([]ConnectionInfo, error) {
			return []ConnectionInfo{
				{ID: "aaaaaaaa-1", Platform: "github", Label: "GitHub – me", Method: "oauth", HasRefreshToken: true},
				{ID: "bbbbbbbb-2", Platform: "notion", Method: "oauth"},
				{ID: "cccccccc-3", Platform: "slack", Method: "api_key"},
			}, nil
		},
		TestConnection: func(_ context.Context, id string) error {
			if id == "cccccccc-3" {
				return nil
			}
			return errors.New("401")
		},
	}
	reg := NewRegistry([]Check{{ID: CheckConnections, Group: GroupAccounts, Title: "Connections", Run: checkConnections}}, accountFixes())
	rep := reg.Run(context.Background(), env, Options{})
	got := byID(rep)
	if got[CheckConnections].Status != StatusWarn {
		t.Fatalf("parent: %+v", got[CheckConnections])
	}
	// OAuth with a refresh token → silent refresh (auto); without → manual reconnect.
	if f := got["accounts.connection.aaaaaaaa"].Fix; f == nil || f.ID != FixConnectionRefresh+":aaaaaaaa-1" || f.Safety != SafetyAuto {
		t.Errorf("refreshable: %+v", f)
	}
	if f := got["accounts.connection.bbbbbbbb"].Fix; f == nil || f.ID != FixReconnect || f.Safety != SafetyManual {
		t.Errorf("no refresh token: %+v", f)
	}
	if got["accounts.connection.cccccccc"].Status != StatusOK {
		t.Errorf("working: %+v", got["accounts.connection.cccccccc"])
	}

	env.Connections = func(context.Context) ([]ConnectionInfo, error) { return nil, nil }
	if res := checkConnections(context.Background(), env); res.Status != StatusInfo {
		t.Errorf("none: %+v", res)
	}
}

func TestAccountChecksAreDeepOnlyExceptLogins(t *testing.T) {
	for _, c := range accountChecks() {
		if want := c.ID != CheckLogins; c.Network != want {
			t.Errorf("%s: Network = %v, want %v", c.ID, c.Network, want)
		}
	}
}

func TestAIProvidersAndLogins(t *testing.T) {
	ctx := context.Background()
	env := &Env{
		AIProviders: func(context.Context) ([]ProviderInfo, error) {
			return []ProviderInfo{{ID: "p1", Name: "ok"}, {ID: "p2", Name: "bad"}}, nil
		},
		TestAIProvider: func(_ context.Context, id string) error {
			if id == "p2" {
				return errors.New("invalid key")
			}
			return nil
		},
		LoginSessions: func(context.Context) ([]SessionInfo, error) {
			return []SessionInfo{
				{Platform: "x", Username: "me", Expiry: time.Now().Add(-time.Hour)},
				{Platform: "instagram", Username: "me", Expiry: time.Now().Add(time.Hour)},
			}, nil
		},
	}
	res := checkAIProviders(ctx, env)
	if res.Status != StatusWarn || len(res.Children) != 2 || res.Children[1].FixID != FixAIProviderKey {
		t.Errorf("providers: %+v", res)
	}
	res = checkLogins(ctx, env)
	if res.Status != StatusWarn || res.Children[0].FixID != FixLogin || res.Children[1].Status != StatusOK {
		t.Errorf("logins: %+v", res)
	}
}
