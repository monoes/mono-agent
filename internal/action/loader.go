package action

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// userActionsDir returns the path to ~/.monoagent/actions where user-installed
// action templates are stored at runtime.
func userActionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".monoagent", "actions")
}

// ActionDef represents a complete action definition: an automation
// package's actions/<action>.json (or a legacy
// ~/.monoagent/actions/<platform>/<action>.json).
type ActionDef struct {
	ActionType  string                 `json:"actionType"`
	Platform    string                 `json:"platform"`
	Version     string                 `json:"version,omitempty"`
	Description string                 `json:"description,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	Inputs      *InputDef              `json:"inputs,omitempty"`
	Outputs     map[string][]string    `json:"outputs,omitempty"`
	Steps       []StepDef              `json:"steps"`
	Loops       []LoopDef              `json:"loops,omitempty"`
	ErrorConfig *GlobalErrorConfig     `json:"errorHandling,omitempty"`

	// --- package-era fields (spec §4.3) ---

	// Schema is the editor "$schema" pointer; ignored at run time.
	Schema string `json:"$schema,omitempty"`
	// Automation is the owning package id; Platform is the legacy alias.
	Automation string `json:"automation,omitempty"`
	// SideEffects is the action's strongest effect:
	// none | read | write | message | destructive.
	SideEffects string `json:"sideEffects,omitempty"`
	// Visibility lists what running the action can reveal to others or leave
	// behind on the site even when it changes nothing, e.g. a profile view
	// the owner sees or a search kept in the account's history. Values:
	// profile_view_visible_to_owner | story_view_visible_to_owner |
	// search_may_be_saved | video_view_counted | account_preference_prompt.
	Visibility []string `json:"visibility,omitempty"`
	// OutputSchema is a JSON Schema describing one output item.
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	// Provenance records how the action was made (e.g. a recording id).
	Provenance map[string]interface{} `json:"provenance,omitempty"`
}

// InputDef lists the required and optional input variables for an action.
// Both fields accept either an array of strings (legacy) or an array of
// objects with {name, type, description, ...} (current format).
type InputDef struct {
	Required []json.RawMessage `json:"required,omitempty"`
	Optional []json.RawMessage `json:"optional,omitempty"`
}

// GlobalErrorConfig defines top-level error handling policy for the entire
// action.
type GlobalErrorConfig struct {
	GlobalRetries  int    `json:"globalRetries,omitempty"`
	RetryDelay     int    `json:"retryDelay,omitempty"`
	OnFinalFailure string `json:"onFinalFailure,omitempty"`
}

// ActionLoader loads and caches action definitions from the installed
// automation packages (the DefSource) or, without one, from the legacy
// ~/.monoagent/actions directory. It is safe for concurrent use.
type ActionLoader struct {
	cache sync.Map
	// srcCache caches DefSource definitions per key as genEntry, valid only
	// while the source's Generation() is unchanged.
	srcCache sync.Map
}

// genEntry is a DefSource definition cached at one source generation.
type genEntry struct {
	gen string
	def *ActionDef
}

// Generational is optionally implemented by a DefSource: Generation changes
// whenever any installed package changes (install, update, enable/disable,
// removal). Without it the loader consults the source on every Load.
type Generational interface{ Generation() string }

var defaultLoader *ActionLoader
var loaderOnce sync.Once

// GetLoader returns the singleton ActionLoader instance.
func GetLoader() *ActionLoader {
	loaderOnce.Do(func() {
		defaultLoader = &ActionLoader{}
	})
	return defaultLoader
}

// Load returns the action definition for the given platform and actionType.
// Both platform and actionType are lower-cased to match the file naming
// convention: actions/<platform>/<action_type>.json
func (l *ActionLoader) Load(platform, actionType string) (*ActionDef, error) {
	normalPlatform := strings.ToLower(strings.TrimSpace(platform))
	normalType := strings.ToLower(strings.TrimSpace(actionType))
	key := fmt.Sprintf("%s/%s", normalPlatform, normalType)

	if src := CurrentDefSource(); src != nil {
		return l.loadFromSource(src, key, normalPlatform, normalType)
	}

	if cached, ok := l.cache.Load(key); ok {
		return cached.(*ActionDef), nil
	}

	// No registry: only the legacy ~/.monoagent/actions directory (the app
	// no longer embeds any packages), plus the tests' fallback tree.
	if fb := testFallbackFS(); fb != nil {
		if b, err := fs.ReadFile(fb, path.Join(normalPlatform, "actions", normalType+".json")); err == nil {
			return l.parseAndCache(key, normalPlatform, normalType, b)
		}
	}
	userDir := userActionsDir()
	if userDir == "" {
		return nil, fmt.Errorf("action definition not found: %s/%s", normalPlatform, normalType)
	}
	fileData, err := os.ReadFile(filepath.Join(userDir, normalPlatform, normalType+".json"))
	if err != nil {
		return nil, fmt.Errorf("action definition not found: %s/%s", normalPlatform, normalType)
	}

	return l.parseAndCache(key, normalPlatform, normalType, fileData)
}

// loadFromSource loads through the registry, which is authoritative: a
// removed or disabled package's actions must not load from the legacy
// directory, nor from a cache filled before it changed (long-lived processes).
func (l *ActionLoader) loadFromSource(src DefSource, key, platform, actionType string) (*ActionDef, error) {
	gen, hasGen := "", false
	if g, ok := src.(Generational); ok {
		gen, hasGen = g.Generation(), true
		if e, ok := l.srcCache.Load(key); ok && e.(genEntry).gen == gen {
			return e.(genEntry).def, nil
		}
	}
	fileData, err := src.Load(platform, actionType)
	if err != nil {
		l.srcCache.Delete(key)
		return nil, fmt.Errorf("action definition not found: %s/%s: %w", platform, actionType, err)
	}
	def, err := ParseActionDef(fileData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse action definition %s/%s: %w", platform, actionType, err)
	}
	if hasGen {
		l.srcCache.Store(key, genEntry{gen: gen, def: def})
	}
	return def, nil
}

func (l *ActionLoader) parseAndCache(key, platform, actionType string, fileData []byte) (*ActionDef, error) {
	def, err := ParseActionDef(fileData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse action definition %s/%s: %w", platform, actionType, err)
	}
	l.cache.Store(key, def)
	return def, nil
}

// ParseActionDef decodes an action JSON file. "automation" is accepted in
// place of the legacy "platform": when only it is set, it fills Platform.
func ParseActionDef(fileData []byte) (*ActionDef, error) {
	var def ActionDef
	if err := json.Unmarshal(fileData, &def); err != nil {
		return nil, err
	}
	if def.Platform == "" {
		def.Platform = def.Automation
	}
	return &def, nil
}

// ListAvailable returns all available action definitions as
// "<platform>/<ACTION_TYPE>" strings.
func (l *ActionLoader) ListAvailable() ([]string, error) {
	if src := CurrentDefSource(); src != nil {
		return src.List()
	}
	var result []string
	if fb := testFallbackFS(); fb != nil {
		files, _ := fs.Glob(fb, "*/actions/*.json")
		for _, f := range files {
			parts := strings.Split(strings.TrimSuffix(f, ".json"), "/")
			result = append(result, parts[0]+"/"+parts[2])
		}
	}

	// Legacy action files in ~/.monoagent/actions/
	userDir := userActionsDir()
	if userDir != "" {
		userPlatformDirs, _ := os.ReadDir(userDir)
		for _, pd := range userPlatformDirs {
			if !pd.IsDir() {
				continue
			}
			p := pd.Name()
			entries, _ := os.ReadDir(filepath.Join(userDir, p))
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
					result = append(result, fmt.Sprintf("%s/%s", p, strings.TrimSuffix(e.Name(), ".json")))
				}
			}
		}
	}

	return result, nil
}

// Invalidate removes a cached action definition, forcing the next Load call
// for that key to re-read its definition.
func (l *ActionLoader) Invalidate(platform, actionType string) {
	key := fmt.Sprintf("%s/%s", strings.ToLower(platform), strings.ToLower(actionType))
	l.cache.Delete(key)
	l.srcCache.Delete(key)
}

// InvalidateAll clears the entire cache.
func (l *ActionLoader) InvalidateAll() {
	l.cache.Range(func(key, _ interface{}) bool {
		l.cache.Delete(key)
		return true
	})
	l.srcCache.Range(func(key, _ interface{}) bool {
		l.srcCache.Delete(key)
		return true
	})
}

var (
	defSourceMu sync.RWMutex
	defSource   DefSource
)

// SetDefSource installs the definition source used by every loader (the
// automation registry calls this at startup). nil restores the legacy
// ~/.monoagent/actions-only behaviour. Clears the cache.
func SetDefSource(src DefSource) {
	defSourceMu.Lock()
	defSource = src
	defSourceMu.Unlock()
	GetLoader().InvalidateAll()
}

// CurrentDefSource returns the installed DefSource, or nil.
func CurrentDefSource() DefSource {
	defSourceMu.RLock()
	defer defSourceMu.RUnlock()
	return defSource
}

var (
	testFallbackMu sync.RWMutex
	testFallback   fs.FS
)

// SetTestFallback gives the no-registry loader an extra definition tree
// (<automation>/actions/<action>.json), read before ~/.monoagent/actions.
// Only tests call it, to load the official packages' source from the repo
// (automations.FS()); the app never sets one. nil removes it.
func SetTestFallback(fsys fs.FS) {
	testFallbackMu.Lock()
	testFallback = fsys
	testFallbackMu.Unlock()
	GetLoader().InvalidateAll()
}

func testFallbackFS() fs.FS {
	testFallbackMu.RLock()
	defer testFallbackMu.RUnlock()
	return testFallback
}
