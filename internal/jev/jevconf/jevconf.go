// Package jevconf is the one place mono-agent code gets a Jev client: key
// resolution (plan D3), per-profile per-surface enablement and thresholds
// (D2, D4), usage recording (D8) and the suggestion cache.
//
// Settings live in the global `settings` table under profile-qualified keys
// `jev.<profileID>.<surface>.<name>`; there is no profile-scoped prefs store.
package jevconf

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"os"

	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/secrets"
)

// Surface names an implicit integration point that needs `jev enable`.
type Surface string

// Surfaces from plan §5. Workflow nodes use NodeSurface instead.
const (
	ActionFallback Surface = "action_fallback"
	HIL            Surface = "hil"
	PeopleReview   Surface = "people_review"
	Capture        Surface = "capture"
	Inbox          Surface = "inbox"
	PeopleLinks    Surface = "people_links"
	Asks           Surface = "asks"
	Retry          Surface = "retry"
	// Decider is recorded for usage only: the org decider opts in through
	// its own autonomy config (plan D2), never through Enabled.
	Decider Surface = "decider"
	// CLI marks raw `monoagentcli jev ask` calls.
	CLI Surface = "cli"
)

// NodeSurface is the usage label for a workflow node type (always enabled:
// using the node is the opt-in).
func NodeSurface(nodeType string) Surface { return Surface("node:" + nodeType) }

// Surfaces lists the surfaces `jev enable` accepts, in display order.
var Surfaces = []Surface{ActionFallback, HIL, PeopleReview, Capture, Inbox, PeopleLinks, Asks, Retry}

// Egress lists, per surface, the data sent to TypeSafe (plan §5); `jev enable`
// prints it before switching a surface on.
var Egress = map[Surface][]string{
	ActionFallback: {"visible page text", "labels and values of visible controls (no password/file inputs)", "the step's intent"},
	HIL:            {"the item's readonly and editable field values", "the node's auto_decide policy text"},
	PeopleReview:   {"person name, job title, category, platform", "the drafted introduction"},
	Capture:        {"captured page URL and title", "first 6,000 characters of the readable text"},
	Inbox:          {"message subject and body", "sender name"},
	PeopleLinks:    {"both people's name, username, platform, website, job title and bio"},
	Asks:           {"reply subject and body", "the question text of up to 50 waiting asks"},
	Retry:          {"node type", "the error message with URLs' query strings, bearer/API tokens and vault values removed", "attempt number and HTTP status"},
}

// Default thresholds per surface (plan §5), compared against jev.Top.
var DefaultThreshold = map[Surface]float64{
	ActionFallback: 0.5, HIL: 0.9, PeopleReview: 0, Capture: 0.75,
	Inbox: 0.7, PeopleLinks: 0.9, Asks: 0.9, Retry: 0.7,
}

// SecretName is the vault entry the resolver looks up.
const SecretName = "typesafe"

// Key sources reported by ResolveKey.
const (
	SourceConfig = "config"
	SourceVault  = "vault"
	SourceEnv    = "env"
)

// ResolveKey implements plan D3: an explicit value (not an @secret: ref) →
// the profile's vault entry "typesafe" → TYPESAFE_API_KEY → jev.ErrNoAPIKey.
// An explicit @secret:<name> ref is resolved from the vault; if that fails
// the env var still applies. A literal "@secret:…" is never returned.
func ResolveKey(ctx context.Context, db *sql.DB, profileID, explicit string) (key, source string, err error) {
	explicit = strings.TrimSpace(explicit)
	ref := "@secret:" + SecretName
	if explicit != "" && !strings.HasPrefix(explicit, "@secret:") {
		return explicit, SourceConfig, nil
	}
	if explicit != "" {
		ref = explicit
	}
	var vaultErr error
	if db != nil {
		v, err := secrets.Resolve(ctx, db, profileID, ref)
		if err == nil && v != "" && !strings.HasPrefix(v, "@secret:") {
			return v, SourceVault, nil
		}
		vaultErr = err
	}
	if v := strings.TrimSpace(envKey()); v != "" {
		return v, SourceEnv, nil
	}
	if vaultErr != nil {
		return "", "", fmt.Errorf("%w (vault: %v)", jev.ErrNoAPIKey, vaultErr)
	}
	return "", "", jev.ErrNoAPIKey
}

// KeySource reports where ResolveKey would find the profile's key — vault
// (an entry named "typesafe" exists) or env — without decrypting anything,
// so health checks never trigger a keyring or passphrase prompt.
func KeySource(ctx context.Context, db *sql.DB, profileID string) (string, error) {
	if db != nil {
		if entries, err := secrets.List(ctx, db, profileID); err == nil {
			for _, e := range entries {
				if e.Name == SecretName {
					return SourceVault, nil
				}
			}
		}
	}
	if strings.TrimSpace(envKey()) != "" {
		return SourceEnv, nil
	}
	return "", jev.ErrNoAPIKey
}

// NewClient resolves the key for profileID and returns a client whose every
// call is recorded in jev_usage under surface s (db may be nil: no recording).
func NewClient(ctx context.Context, db *sql.DB, profileID, explicitKey, model string, s Surface) (*jev.Client, error) {
	key, _, err := ResolveKey(ctx, db, profileID, explicitKey)
	if err != nil {
		return nil, err
	}
	c, err := jev.NewClient(key, model)
	if err != nil {
		return nil, err
	}
	if db != nil {
		c.OnResult = Recorder(db, profileID, s)
	}
	return c, nil
}

// Recorder returns an OnResult hook writing one jev_usage row per call.
// Recording failures are ignored: bookkeeping must never fail a decision.
func Recorder(db *sql.DB, profileID string, s Surface) func(*jev.Request, *jev.Response, time.Duration, error) {
	return func(req *jev.Request, resp *jev.Response, latency time.Duration, err error) {
		model, tokens, ok := req.Model, 0, 1
		if resp != nil {
			tokens = resp.Usage.InputTokens
			if resp.Model != "" {
				model = resp.Model
			}
		}
		if err != nil {
			ok = 0
		}
		_, _ = db.Exec(`INSERT INTO jev_usage (profile_id, surface, model, questions, input_tokens, latency_ms, ok)
			VALUES (?,?,?,?,?,?,?)`, profileID, string(s), model, len(req.Questions), tokens, latency.Milliseconds(), ok)
	}
}

func envKey() string { return os.Getenv("TYPESAFE_API_KEY") }

func settingKey(profileID string, s Surface, name string) string {
	if profileID == "" {
		profileID = "default"
	}
	return "jev." + profileID + "." + string(s) + "." + name
}

func getSetting(db *sql.DB, key string) (string, bool) {
	if db == nil {
		return "", false
	}
	var v string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func setSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(`INSERT INTO settings (key, value) VALUES (?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Known reports whether s is a surface `jev enable` accepts.
func Known(s Surface) bool {
	for _, k := range Surfaces {
		if k == s {
			return true
		}
	}
	return false
}

// Enabled reports whether the profile switched surface s on. Off by default;
// a nil db means off.
func Enabled(db *sql.DB, profileID string, s Surface) bool {
	v, ok := getSetting(db, settingKey(profileID, s, "enabled"))
	return ok && v == "true"
}

// SetEnabled switches surface s on or off for the profile.
func SetEnabled(db *sql.DB, profileID string, s Surface, on bool) error {
	if !Known(s) {
		return fmt.Errorf("jev: unknown surface %q", s)
	}
	return setSetting(db, settingKey(profileID, s, "enabled"), strconv.FormatBool(on))
}

// Threshold returns the profile's gate value for s, else def.
func Threshold(db *sql.DB, profileID string, s Surface, def float64) float64 {
	v, ok := getSetting(db, settingKey(profileID, s, "threshold"))
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 || f > 1 {
		return def
	}
	return f
}

// SetThreshold stores a gate value in (0,1] for s.
func SetThreshold(db *sql.DB, profileID string, s Surface, v float64) error {
	if !Known(s) {
		return fmt.Errorf("jev: unknown surface %q", s)
	}
	if v <= 0 || v > 1 {
		return fmt.Errorf("jev: threshold %v must be in (0,1]", v)
	}
	return setSetting(db, settingKey(profileID, s, "threshold"), strconv.FormatFloat(v, 'f', -1, 64))
}

// SaveSuggestion caches v (JSON-encoded) as the latest suggestion for subject.
func SaveSuggestion(db *sql.DB, profileID string, s Surface, subjectID string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO jev_suggestions (profile_id, surface, subject_id, answer) VALUES (?,?,?,?)
		ON CONFLICT(profile_id, surface, subject_id) DO UPDATE SET answer = excluded.answer,
		created_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`, profileID, string(s), subjectID, string(raw))
	return err
}

// LoadSuggestion decodes the cached suggestion for subject into v.
func LoadSuggestion(db *sql.DB, profileID string, s Surface, subjectID string, v any) (bool, error) {
	var raw string
	err := db.QueryRow(`SELECT answer FROM jev_suggestions WHERE profile_id = ? AND surface = ? AND subject_id = ?`,
		profileID, string(s), subjectID).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), v)
}

// Usage is an aggregate over jev_usage.
type Usage struct {
	Surface      string  `json:"surface"`
	Calls        int     `json:"calls"`
	Failures     int     `json:"failures"`
	InputTokens  int     `json:"input_tokens"`
	EstimatedUSD float64 `json:"estimated_usd"`
	AvgLatencyMS int     `json:"avg_latency_ms"`
}

// USDPerInputToken is TypeSafe's list price ($0.042 per million input tokens).
const USDPerInputToken = 0.042 / 1e6

// UsageSince aggregates the profile's calls since t, by surface.
func UsageSince(db *sql.DB, profileID string, since time.Time) ([]Usage, error) {
	rows, err := db.Query(`SELECT surface, COUNT(*), SUM(1-ok), COALESCE(SUM(input_tokens),0), COALESCE(AVG(latency_ms),0)
		FROM jev_usage WHERE profile_id = ? AND created_at >= ? GROUP BY surface ORDER BY surface`,
		profileID, since.UTC().Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Usage
	for rows.Next() {
		var u Usage
		var avg float64
		if err := rows.Scan(&u.Surface, &u.Calls, &u.Failures, &u.InputTokens, &avg); err != nil {
			return nil, err
		}
		u.AvgLatencyMS = int(avg)
		u.EstimatedUSD = float64(u.InputTokens) * USDPerInputToken
		out = append(out, u)
	}
	return out, rows.Err()
}
