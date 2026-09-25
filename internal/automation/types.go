// Package automation implements browser automation packages: the manifest,
// the package loader (directory, .mpkg zip, embedded seed), the installed
// registry under ~/.monoagent/automations, install/export/seed, validation
// and the social policy gate.
//
// Spec: docs/mastermind/specs/2026-09-25-browser-automation-packages-design.md
// Contracts: docs/mastermind/plans/2026-09-25-browser-automation-packages-contracts.md
package automation

import "time"

// SchemaV1 is the manifest "schema" value.
const SchemaV1 = "monoagent.automation/v1"

// Package sources.
const (
	SourceBuiltin  = "builtin"
	SourceImported = "imported"
	SourceLocal    = "local"
)

// Trust tiers (index "trust"; contracts §8). Only builtin and local get the
// bare-name vault fallback and scripts by default.
const (
	TrustBuiltin  = "builtin"
	TrustLocal    = "local"
	TrustRecorded = "recorded" // saved from `record save`
	TrustImported = "imported"
)

// Manifest is automation.json.
type Manifest struct {
	SchemaRef   string         `json:"$schema,omitempty"`
	Schema      string         `json:"schema"`
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description,omitempty"`
	Publisher   *Publisher     `json:"publisher,omitempty"`
	License     string         `json:"license,omitempty"`
	Engine      string         `json:"engine,omitempty"` // semver range, e.g. ">=0.68.0"
	Category    string         `json:"category,omitempty"`
	Icon        string         `json:"icon,omitempty"` // file in the package
	Site        Site           `json:"site"`
	Login       *Login         `json:"login,omitempty"`
	Permissions Permissions    `json:"permissions"`
	Requires    Requires       `json:"requires,omitempty"`
	Actions     []string       `json:"actions"`
	Defaults    map[string]any `json:"defaults,omitempty"`
	Policy      Policy         `json:"policy"`
}

type Publisher struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type Site struct {
	StartURL string   `json:"startUrl"`
	Domains  []string `json:"domains"`
}

// Login describes how to log in and how to tell a session is logged in.
type Login struct {
	URL            string     `json:"url"`
	LoggedIn       *Probe     `json:"loggedIn,omitempty"`
	UsernameFrom   *AttrProbe `json:"usernameFrom,omitempty"`
	SessionTTLDays int        `json:"sessionTtlDays,omitempty"`
}

type Probe struct {
	Selector string `json:"selector,omitempty"`
	Cookie   string `json:"cookie,omitempty"`
}

type AttrProbe struct {
	Selector  string `json:"selector"`
	Attribute string `json:"attribute,omitempty"` // "" = innerText
}

type Permissions struct {
	Steps     []string `json:"steps"`   // step types / prefix globs; empty = unrestricted (built-ins only)
	Scripts   []string `json:"scripts"` // declared scripts/<name>.js files
	Downloads bool     `json:"downloads"`
	// CallActions lists the exact "<automation>.<action>" refs this package
	// may call_action (own-package calls need no declaration).
	CallActions []string `json:"callActions,omitempty"`
}

type Requires struct {
	Native    string   `json:"native,omitempty"` // compiled Go bot id, e.g. "instagram"
	Fragments []string `json:"fragments,omitempty"`
}

type Policy struct {
	Tier string `json:"tier"` // "standard" | "social"
}

// InstalledInfo is one row of `automation list --json`.
type InstalledInfo struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Version           string    `json:"version"`
	Description       string    `json:"description,omitempty"`
	Category          string    `json:"category,omitempty"`
	Source            string    `json:"source"` // builtin | imported | local
	Trust             string    `json:"trust"`  // builtin | local | recorded | imported
	Enabled           bool      `json:"enabled"`
	Removed           bool      `json:"removed,omitempty"`  // uninstalled built-in (only with --all)
	Modified          bool      `json:"modified,omitempty"` // differs from its seeded copy
	Available         bool      `json:"available"`
	UnavailableReason string    `json:"unavailableReason,omitempty"`
	ContainsScripts   bool      `json:"containsScripts"`
	Actions           int       `json:"actions"`
	Domains           []string  `json:"domains,omitempty"`
	StartURL          string    `json:"startUrl,omitempty"`
	Tier              string    `json:"tier"`
	HasIcon           bool      `json:"hasIcon,omitempty"`
	Dir               string    `json:"dir"`
	InstalledAt       time.Time `json:"installedAt"`
	PreviousVersion   string    `json:"previousVersion,omitempty"` // rollback target
	PendingUpdate     string    `json:"pendingUpdate,omitempty"`   // newer seed held back because the user modified this built-in
	ScriptsAllowed    bool      `json:"scriptsAllowed"`            // page_script / http_fetch_in_page may run
	LiveRunConfirmed  bool      `json:"liveRunConfirmed"`          // write-level actions may run for real
}

// InstallOptions controls Install.
type InstallOptions struct {
	DryRun bool   // validate + review only, write nothing
	Source string // SourceImported (default) or SourceLocal
	// Trust overrides the trust tier recorded for the package (e.g.
	// TrustRecorded from `record save`). Default: derived from Source.
	Trust string
	// ReplaceBuiltin allows an imported package to replace an installed
	// built-in or local package with the same id.
	ReplaceBuiltin bool
	// ExpectSHA256 pins the package bytes: when set, install refuses unless
	// the fetched archive (or the packed directory) hashes to it. Pass the
	// SHA256 of a dry-run result to install exactly what was reviewed.
	ExpectSHA256 string
}

// InstallResult is returned by Install/AddAction (and `--json`).
type InstallResult struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Version         string      `json:"version"`
	PreviousVersion string      `json:"previousVersion,omitempty"`
	DryRun          bool        `json:"dryRun"`
	SHA256          string      `json:"sha256,omitempty"` // hash of the exact package bytes reviewed
	Review          Review      `json:"review"`
	Warnings        []string    `json:"warnings,omitempty"`
	Issues          []IssueJSON `json:"issues,omitempty"`
	Installed       bool        `json:"installed"`
	Dir             string      `json:"dir,omitempty"`
}

// Review is what the install confirmation shows (spec §6.3).
type Review struct {
	Source         string            `json:"source"`
	Trust          string            `json:"trust"`
	Replaces       *Replaced         `json:"replaces,omitempty"` // installed package this install overwrites
	ComputedTier   string            `json:"computedTier"`       // "social" when the gate applies, whatever the manifest says
	SocialPlatform string            `json:"socialPlatform,omitempty"`
	Native         string            `json:"native,omitempty"`
	CallActions    []string          `json:"callActions"`
	LoginURL       string            `json:"loginURL,omitempty"`
	ScriptSources  map[string]string `json:"scriptSources"` // scripts/<name> → full source text
	Capabilities   []string          `json:"capabilities"`  // plain-language list of what the package can do
	Publisher      string            `json:"publisher,omitempty"`
	Domains        []string          `json:"domains"`
	Steps          []string          `json:"steps"`
	Scripts        []string          `json:"scripts"`
	Downloads      bool              `json:"downloads"`
	Tier           string            `json:"tier"`
	ActionEffects  map[string]string `json:"actionEffects"` // action → sideEffects
	Files          []FileInfo        `json:"files"`
	PolicyBlocked  bool              `json:"policyBlocked"`
	PolicyReason   string            `json:"policyReason,omitempty"`
	Changes        *ReviewChanges    `json:"changes,omitempty"` // on update: permission/script diff
}

// Replaced names the installed package an install overwrites.
type Replaced struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Trust   string `json:"trust"`
	Version string `json:"version"`
}

type ReviewChanges struct {
	AddedDomains     []string `json:"addedDomains,omitempty"`
	AddedSteps       []string `json:"addedSteps,omitempty"`
	AddedCallActions []string `json:"addedCallActions,omitempty"`
	AddedScripts     []string `json:"addedScripts,omitempty"`
	ChangedScripts   []string `json:"changedScripts,omitempty"`
}

type FileInfo struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// IssueJSON mirrors action.Issue plus the file it was found in.
type IssueJSON struct {
	File     string `json:"file,omitempty"`
	Severity string `json:"severity"`
	StepID   string `json:"stepId,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// ExportOptions controls Export.
type ExportOptions struct {
	Actions        []string // subset; empty = all (closure is computed)
	WithRecordings bool
}
