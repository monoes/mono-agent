package main

// OpenAI-compatible API settings (Settings page → "OpenAI-compatible API").
// Every method shells out to `monoagentcli api …` through runMonoCLI; this
// file never touches the key store, the database or the gateway. A key
// created here is printed once by the CLI, in `api key create --json`, and
// travels no further than the create dialog: no other method decodes one.

import (
	"errors"
	"strconv"
	"strings"
)

// APIKey is one key of the active profile (`api key list|update|revoke --json`).
// The key itself is never part of it.
type APIKey struct {
	ID         string `json:"id"`
	ProfileID  string `json:"profile_id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Context    bool   `json:"context"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at"` // "" when the key was never used
	RevokedAt  string `json:"revoked_at"`
}

// APIKeyCreated mirrors `api key create --json`: the key's metadata plus the
// key itself, which the CLI prints this once.
type APIKeyCreated struct {
	APIKey
	Key string `json:"key"`
}

// APIListener is one listener of `api status --json`.
type APIListener struct {
	Name               string `json:"name"` // main | v1
	Addr               string `json:"addr"`
	Loopback           bool   `json:"loopback"`
	V1                 bool   `json:"v1"` // the listener is meant to serve /v1
	Confinement        string `json:"confinement"`
	ContextConfinement string `json:"context_confinement"`
	AutoConfinement    string `json:"auto_confinement"`
	ConfinementSource  string `json:"confinement_source"` // daemon | environment
	Reachable          bool   `json:"reachable"`
	V1Answers          bool   `json:"v1_answers"`
}

// APIStatusKeys is the key count of `api status --json`.
type APIStatusKeys struct {
	Active int `json:"active"`
}

// APIStatusDaemon is the daemon part of `api status --json`.
type APIStatusDaemon struct {
	Running bool   `json:"running"`
	APIAddr string `json:"api_addr"`
	V1Addr  string `json:"v1_addr"`
}

// APIAuto says whether the auto model works for the profile and what is
// missing when it does not (`api status|models --json`; Confinement,
// Candidates and HeldBack are `api models` only).
type APIAuto struct {
	Available   bool   `json:"available"`
	Missing     string `json:"missing"`
	KeySource   string `json:"key_source"`
	Confinement string `json:"confinement"`
	Candidates  int    `json:"candidates"`
	HeldBack    int    `json:"held_back"`
}

// APIStatusInfo mirrors `api status --json`.
type APIStatusInfo struct {
	Profile   string          `json:"profile"`
	Keys      APIStatusKeys   `json:"keys"`
	Daemon    APIStatusDaemon `json:"daemon"`
	Auto      APIAuto         `json:"auto"`
	Listeners []APIListener   `json:"listeners"`
}

// APIModel is one model of `api models --json`.
type APIModel struct {
	ID             string `json:"id"`
	Runtime        string `json:"runtime"`
	Model          string `json:"model"`
	Label          string `json:"label"`
	Confinement    string `json:"confinement"` // chat-only | sandboxed | unconfined
	Validated      bool   `json:"validated"`
	Allowed        bool   `json:"allowed"`
	ContextAllowed bool   `json:"context_allowed"`
	AutoAllowed    bool   `json:"auto_allowed"`
}

// APIPolicy is the policy `api models --json` evaluated.
type APIPolicy struct {
	For                string `json:"for"` // loopback | network
	Confinement        string `json:"confinement"`
	ContextConfinement string `json:"context_confinement"`
	AutoConfinement    string `json:"auto_confinement"`
	Source             string `json:"source"`
}

// APIModelsInfo mirrors `api models --json`.
type APIModelsInfo struct {
	Policy APIPolicy  `json:"policy"`
	Models []APIModel `json:"models"`
	Auto   APIAuto    `json:"auto"`
}

// apiKeyID refuses an empty id and anything that would parse as a flag.
func apiKeyID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("a key id is required")
	}
	if strings.HasPrefix(id, "-") {
		return "", errors.New("invalid key id " + strconv.Quote(id))
	}
	return id, nil
}

// APIStatus reports where the API listens and whether it answers.
func (a *App) APIStatus() (APIStatusInfo, error) {
	var st APIStatusInfo
	if err := a.runMonoCLI("", &st, "api", "status"); err != nil {
		return APIStatusInfo{}, err
	}
	if st.Listeners == nil {
		st.Listeners = []APIListener{}
	}
	return st, nil
}

// APIModels lists the models the API would serve, for the policy of a
// loopback or a network listener (forListener, "" for the CLI's default) and
// the classes the running server applies (confinement, contextConfinement and
// autoConfinement, "" for this app's own environment). Each value is attached
// to its flag, so it can never be read as another one.
func (a *App) APIModels(forListener, confinement, contextConfinement, autoConfinement string) (APIModelsInfo, error) {
	args := []string{"api", "models"}
	for _, f := range [][2]string{
		{"for", forListener}, {"confinement", confinement},
		{"context-confinement", contextConfinement}, {"auto-confinement", autoConfinement},
	} {
		if v := strings.TrimSpace(f[1]); v != "" {
			args = append(args, "--"+f[0]+"="+v)
		}
	}
	var m APIModelsInfo
	if err := a.runMonoCLI("", &m, args...); err != nil {
		return APIModelsInfo{}, err
	}
	if m.Models == nil {
		m.Models = []APIModel{}
	}
	return m, nil
}

// APIKeyList lists the active profile's active keys.
func (a *App) APIKeyList() ([]APIKey, error) {
	var keys []APIKey
	if err := a.runMonoCLI("", &keys, "api", "key", "list"); err != nil {
		return nil, err
	}
	if keys == nil {
		keys = []APIKey{}
	}
	return keys, nil
}

// APIKeyCreate creates a key. The result carries the key itself, which the CLI
// prints once: the caller shows it once and keeps it nowhere.
func (a *App) APIKeyCreate(name string, withContext bool) (APIKeyCreated, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return APIKeyCreated{}, errors.New("enter a name for the key")
	}
	args := []string{"api", "key", "create", "--name=" + name}
	if withContext {
		args = append(args, "--context")
	}
	var created APIKeyCreated
	if err := a.runMonoCLI("", &created, args...); err != nil {
		return APIKeyCreated{}, err
	}
	return created, nil
}

// APIKeySetContext switches a key's knowledge context on or off.
func (a *App) APIKeySetContext(id string, on bool) (APIKey, error) {
	id, err := apiKeyID(id)
	if err != nil {
		return APIKey{}, err
	}
	flag := "--no-context"
	if on {
		flag = "--context"
	}
	var key APIKey
	if err := a.runMonoCLI("", &key, "api", "key", "update", id, flag); err != nil {
		return APIKey{}, err
	}
	return key, nil
}

// APIKeyRevoke revokes a key. The GUI asks before calling this, which stands
// in for the CLI's own prompt (--yes).
func (a *App) APIKeyRevoke(id string) (APIKey, error) {
	id, err := apiKeyID(id)
	if err != nil {
		return APIKey{}, err
	}
	var key APIKey
	if err := a.runMonoCLI("", &key, "api", "key", "revoke", id, "--yes"); err != nil {
		return APIKey{}, err
	}
	return key, nil
}
