package main

// OpenAI-compatible API settings (Settings page → "OpenAI-compatible API").
// Every method shells out to `monoagentcli api …` through runAPICLI; this
// file never touches the key store, the database or the gateway. A key
// created here is printed once by the CLI, in `api key create --json`, and
// travels no further than the create dialog: no other method decodes one.
//
// What a CLI that predates a field does not send stays absent in what the
// page receives (a pointer, or omitempty), so that the page can tell "the CLI
// said no" from "the CLI said nothing".

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
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
	AutoConfinement    string `json:"auto_confinement,omitempty"` // an older CLI sends none
	ConfinementSource  string `json:"confinement_source"`         // daemon | environment
	Scheme             string `json:"scheme,omitempty"`           // http | https, the one that answered; none when it did not (or an older CLI)
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
// Candidates and HeldBack are `api models` only, and the CLI leaves out a
// count of 0).
type APIAuto struct {
	Available   bool   `json:"available"`
	Missing     string `json:"missing,omitempty"`
	KeySource   string `json:"key_source,omitempty"`
	Confinement string `json:"confinement,omitempty"` // a class: chat-only | sandboxed | unconfined
	Candidates  *int   `json:"candidates,omitempty"`
	HeldBack    *int   `json:"held_back,omitempty"`
}

// APIStatusInfo mirrors `api status --json`.
type APIStatusInfo struct {
	Profile   string          `json:"profile"`
	Keys      APIStatusKeys   `json:"keys"`
	Daemon    APIStatusDaemon `json:"daemon"`
	Auto      *APIAuto        `json:"auto,omitempty"` // an older CLI sends none
	Listeners []APIListener   `json:"listeners"`
}

// APIModel is one model of `api models --json`.
type APIModel struct {
	ID             string   `json:"id"`
	Runtime        string   `json:"runtime"`
	Model          string   `json:"model"`
	Label          string   `json:"label"`
	Confinement    string   `json:"confinement"` // chat-only | sandboxed | unconfined
	Validated      bool     `json:"validated"`
	Allowed        bool     `json:"allowed"`
	ContextAllowed bool     `json:"context_allowed"`
	AutoAllowed    *bool    `json:"auto_allowed,omitempty"` // an older CLI sends none
	Capabilities   []string `json:"capabilities,omitempty"` // what it can do: text, image, tools; an older CLI sends none
}

// APIPolicy is the policy `api models --json` evaluated.
type APIPolicy struct {
	For                string `json:"for"` // loopback | network
	Confinement        string `json:"confinement"`
	ContextConfinement string `json:"context_confinement"`
	AutoConfinement    string `json:"auto_confinement,omitempty"` // an older CLI sends none
	Source             string `json:"source"`
}

// APIModelsInfo mirrors `api models --json`.
type APIModelsInfo struct {
	Policy APIPolicy  `json:"policy"`
	Models []APIModel `json:"models"`
	Auto   *APIAuto   `json:"auto,omitempty"`
}

// apiCLIError is a failed `monoagentcli api …` call: its exit code
// (cmd/monoagentcli/exitcodes.go: 2 not found, 3 invalid input) and the line it
// printed last. The page needs the class to word the failure, and the only thing
// a Wails error carries is its text, so the classes the CLI itself names in its
// JSON errors lead the text: "not_found: …", "invalid_input: …".
type apiCLIError struct {
	code int
	msg  string
}

func (e *apiCLIError) Error() string {
	switch e.code {
	case 2:
		return "not_found: " + e.msg
	case 3:
		return "invalid_input: " + e.msg
	}
	return e.msg
}

// runAPICLI is runMonoCLI (--profile <active> --json, decoded stdout) keeping the
// exit code. The message is the last stderr line: the CLI prints its error last,
// after any warnings, such as the migrations a first run logs.
func (a *App) runAPICLI(result interface{}, args ...string) error {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil { // before startup, and in tests
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, cliBin, append([]string{"--profile", a.getActiveProfileID(), "--json"}, args...)...)
	suppressConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			lines := strings.Split(strings.TrimSpace(string(ee.Stderr)), "\n")
			msg := strings.TrimSpace(lines[len(lines)-1])
			if msg == "" {
				msg = err.Error()
			}
			return &apiCLIError{code: ee.ExitCode(), msg: msg}
		}
		return err
	}
	return json.Unmarshal(out, result)
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
	if err := a.runAPICLI(&st, "api", "status"); err != nil {
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
	if err := a.runAPICLI(&m, args...); err != nil {
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
	if err := a.runAPICLI(&keys, "api", "key", "list"); err != nil {
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
	if err := a.runAPICLI(&created, args...); err != nil {
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
	if err := a.runAPICLI(&key, "api", "key", "update", id, flag); err != nil {
		return APIKey{}, err
	}
	return key, nil
}

// APIKeyRename renames an active key (`api key update <id> --name=<name>`). It
// changes the name and nothing else: the context switch is not touched, and the
// key itself is neither needed nor shown (the CLI answers with the key's
// metadata, which is all this decodes). The name rule and the clash with another
// active key are the store's: the CLI refuses with exit 3 and the page words it.
func (a *App) APIKeyRename(id, name string) (APIKey, error) {
	id, err := apiKeyID(id)
	if err != nil {
		return APIKey{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return APIKey{}, errors.New("enter a name for the key")
	}
	var key APIKey
	if err := a.runAPICLI(&key, "api", "key", "update", id, "--name="+name); err != nil {
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
	if err := a.runAPICLI(&key, "api", "key", "revoke", id, "--yes"); err != nil {
		return APIKey{}, err
	}
	return key, nil
}
