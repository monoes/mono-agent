package action

// Contracts between the action engine and browser automation packages
// (internal/automation). The action package never imports internal/automation;
// a package hands itself to the executor through PackageContext, and the
// registry hands definitions to the loader through DefSource.
//
// See docs/mastermind/plans/2026-09-25-browser-automation-packages-contracts.md.

// PackageContext is the view of one automation package that the executor
// needs while running one of its actions. A nil PackageContext means a
// legacy action with no package: no domain allowlist, no fragments, no
// scripts, no package selectors.
type PackageContext interface {
	// ID is the automation id, e.g. "hackernews".
	ID() string
	// StartURL is manifest.site.startUrl ("" when unset).
	StartURL() string
	// Domains is manifest.site.domains: exact hosts or "*.example.com"
	// globs. Empty means unrestricted (built-ins without a declared site).
	Domains() []string
	// PermittedSteps is manifest.permissions.steps. Entries are step types,
	// or a prefix glob such as "extract_*". Empty means unrestricted.
	PermittedSteps() []string
	// Fragment returns fragments/<name>.json.
	Fragment(name string) (*FragmentDef, error)
	// Selector returns selectors.json[key] (a local overlay, if any, wins).
	Selector(key string) (*SelectorEntry, bool)
	// Script returns the source of scripts/<name> (name includes ".js").
	Script(name string) (string, error)
	// ResolveAction resolves a call_action reference: "<action>" within this
	// package, or "<automation>.<action>" in another installed package.
	ResolveAction(ref string) (*ActionDef, PackageContext, error)
}

// FragmentDef is a reusable step sequence (fragments/<name>.json). It is
// included by a call_fragment step and is never a node on its own.
type FragmentDef struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Inputs      *InputDef `json:"inputs,omitempty"`
	Steps       []StepDef `json:"steps"`
}

// SelectorEntry is one named selector in selectors.json: ranked candidates
// tried in order, plus the plain-words intent for the Jev fallback.
type SelectorEntry struct {
	Candidates []SelectorCandidate `json:"candidates"`
	Intent     string              `json:"intent,omitempty"`
	VerifiedAt string              `json:"verifiedAt,omitempty"`
}

// SelectorCandidate is exactly one of CSS, XPath, Aria or Text.
type SelectorCandidate struct {
	CSS   string        `json:"css,omitempty"`
	XPath string        `json:"xpath,omitempty"`
	Aria  *AriaSelector `json:"aria,omitempty"`
	Text  string        `json:"text,omitempty"` // visible text, exact after trim
	Score float64       `json:"score,omitempty"`
}

// AriaSelector matches by ARIA role and accessible name.
type AriaSelector struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

// WaitSpec is the condition of a wait_for step, or a step's "until"
// outcome. Exactly one of the leaf fields, or Any (first match wins).
type WaitSpec struct {
	URLMatches  string     `json:"urlMatches,omitempty"` // Go regexp against the page URL
	Selector    string     `json:"selector,omitempty"`   // CSS; element present
	Text        string     `json:"text,omitempty"`       // substring of body innerText
	NetworkIdle bool       `json:"networkIdle,omitempty"`
	Gone        string     `json:"gone,omitempty"` // CSS; element absent
	Any         []WaitSpec `json:"any,omitempty"`
}

// TransformOp is one operation of a transform step, applied in order to the
// list (or single object) the step's Input resolves to.
//
//	map          Map: {newField: "{{template over the item}}"}
//	filter       Where: condition evaluated against each item
//	dedupe       Field: key field (whole item when empty)
//	regex_extract Field, Pattern (first capture group, else whole match), To
//	parse_date   Field, Layout (Go layout, or "auto"), To (RFC3339 output)
//	parse_number Field, To ("1.2k" → 1200, "3,400" → 3400)
//	join         Field (list field), Sep, To
//	split        Field, Sep, To
//	pick         Fields: keep only these keys
//	limit        Count
//	lower        Field, To
//	replace      Field, Pattern (Go regexp), With, To
//	tree_parent  Field (depth), ID, To, Root, Carry
//	index        To, Carry (numbers the rows 0, 1, …; with Carry the count continues across pages)
//	flag         Where, To (per-item true/false from the condition)
//	sort         Field, Order (stable; numeric when both values are numbers, else string; missing last)
//	add, subtract, multiply, divide  Field, By, Round, To (numbers parsed like parse_number)
type TransformOp struct {
	Op      string            `json:"op"`
	Field   string            `json:"field,omitempty"`
	To      string            `json:"to,omitempty"`
	Pattern string            `json:"pattern,omitempty"`
	Layout  string            `json:"layout,omitempty"`
	Sep     string            `json:"sep,omitempty"`
	Where   *ConditionDef     `json:"where,omitempty"`
	Map     map[string]string `json:"map,omitempty"`
	Fields  []string          `json:"fields,omitempty"`
	Count   int               `json:"count,omitempty"`
	With    string            `json:"with,omitempty"`  // replace: replacement text ($1 expands)
	ID      string            `json:"id,omitempty"`    // tree_parent: the row's id field
	Root    string            `json:"root,omitempty"`  // tree_parent: template, parent of depth-0 rows
	Carry   string            `json:"carry,omitempty"` // tree_parent: variable holding the open-ancestor stack across pages; index: the row count so far
	Order   string            `json:"order,omitempty"` // sort: "asc" (default) | "desc"
	By      float64           `json:"by,omitempty"`    // add/subtract/multiply/divide: the operand
	Round   bool              `json:"round,omitempty"` // add/subtract/multiply/divide: round the result to an integer
}

// SelectorObserver receives the outcome of every package-selector lookup
// (selector health, spec §8.7). candidateIndex is the index that matched,
// or -1 when none did. healed is true when a non-first candidate or the Jev
// fallback found the element.
type SelectorObserver interface {
	ObserveSelector(automationID, key string, candidateIndex int, ok, healed bool)
}

// SelectorCandidateObserver is optionally implemented by a SelectorObserver
// that wants the matched candidate itself (the index is relative to the
// entry the run saw, which may since have been reordered). c is nil when no
// candidate matched.
type SelectorCandidateObserver interface {
	ObserveSelectorCandidate(automationID, key string, c *SelectorCandidate, candidateIndex int, ok, healed bool)
}

// DefSource supplies action definitions to the loader. The automation
// registry installs one at startup (SetDefSource); with none set the loader
// reads the embedded seed (data.AutomationsFS) and the legacy
// ~/.monoagent/actions directory.
type DefSource interface {
	// Load returns the raw action JSON for automation/action.
	Load(automation, actionType string) ([]byte, error)
	// List returns "<automation>/<action>" entries.
	List() ([]string, error)
	// Package returns the package context for an automation, or nil.
	Package(automation string) PackageContext
}
