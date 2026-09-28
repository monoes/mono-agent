package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/secrets"
)

// VaultSecretName is the vault entry holding a profile's monoes.me login.
const VaultSecretName = "monoes-library"

const vaultTokenField = "token"

// VaultStore keeps one profile's login in its encrypted vault, one entry
// per library host (the entry's URL), like the HTTP API's bearer token.
type VaultStore struct {
	DB        *sql.DB
	ProfileID string
	BaseURL   string
}

func (s *VaultStore) find(ctx context.Context) (string, error) {
	entries, err := secrets.List(ctx, s.DB, s.ProfileID)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Kind == "secret" && e.Name == VaultSecretName && e.URL == s.BaseURL {
			return e.ID, nil
		}
	}
	return "", nil
}

// Load returns the stored login, or nil when there is none.
func (s *VaultStore) Load(ctx context.Context) (*Token, error) {
	id, err := s.find(ctx)
	if err != nil || id == "" {
		return nil, err
	}
	fields, _, err := secrets.DecryptFields(ctx, s.DB, s.ProfileID, id)
	if err != nil {
		return nil, err
	}
	var t Token
	if err := json.Unmarshal([]byte(fields[vaultTokenField]), &t); err != nil || t.AccessToken == "" {
		return nil, fmt.Errorf("the stored monoes.me login is unreadable; run `monoagentcli library login` again")
	}
	return &t, nil
}

// Save stores t, replacing the profile's login for this host.
func (s *VaultStore) Save(ctx context.Context, t *Token) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	fields := map[string]string{vaultTokenField: string(b)}
	username := ""
	if t.User != nil {
		username = t.User.Username
		if username == "" {
			username = t.User.Email
		}
	}
	id, err := s.find(ctx)
	if err != nil {
		return err
	}
	if id != "" {
		return secrets.Update(ctx, s.DB, s.ProfileID, id, nil, &username, nil, nil, fields)
	}
	_, err = secrets.Add(ctx, s.DB, s.ProfileID, "secret", VaultSecretName, fields, username, s.BaseURL,
		"monoes.me library login (monoagentcli library login)")
	return err
}

// Delete forgets the login.
func (s *VaultStore) Delete(ctx context.Context) error {
	id, err := s.find(ctx)
	if err != nil || id == "" {
		return err
	}
	return secrets.Delete(ctx, s.DB, s.ProfileID, id)
}
