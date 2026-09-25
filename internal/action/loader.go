package action

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/monoes/mono-agent/data"
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

// ActionDef represents a complete action definition loaded from an embedded
// JSON file under data/actions/<platform>/<TYPE>.json.
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

// ActionLoader loads and caches action definitions from the embedded JSON files
// in data.AutomationsFS. It is safe for concurrent use.
type ActionLoader struct {
	cache sync.Map
}

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

	if cached, ok := l.cache.Load(key); ok {
		return cached.(*ActionDef), nil
	}

	var fileData []byte
	var err error
	if src := CurrentDefSource(); src != nil {
		// The registry is authoritative: a removed or disabled package's
		// actions must not load from the embedded seed.
		fileData, err = src.Load(normalPlatform, normalType)
		if err != nil {
			return nil, fmt.Errorf("action definition not found: %s/%s: %w", normalPlatform, normalType, err)
		}
		return l.parseAndCache(key, normalPlatform, normalType, fileData)
	}

	path := fmt.Sprintf("automations/%s/actions/%s.json", normalPlatform, normalType)
	fileData, err = data.AutomationsFS.ReadFile(path)
	if err != nil {
		// Fall back to user-installed templates in ~/.monoagent/actions/
		userDir := userActionsDir()
		if userDir != "" {
			userPath := filepath.Join(userDir, normalPlatform, normalType+".json")
			fileData, err = os.ReadFile(userPath)
		}
		if err != nil {
			return nil, fmt.Errorf("action definition not found: %s/%s", normalPlatform, normalType)
		}
	}

	return l.parseAndCache(key, normalPlatform, normalType, fileData)
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

	// Dynamically discover all platform directories under actions/
	platformDirs, err := data.AutomationsFS.ReadDir("automations")
	if err != nil {
		return nil, fmt.Errorf("list platforms: %w", err)
	}

	for _, pd := range platformDirs {
		if !pd.IsDir() {
			continue
		}
		p := pd.Name()
		entries, err := data.AutomationsFS.ReadDir(fmt.Sprintf("automations/%s/actions", p))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				name := strings.TrimSuffix(e.Name(), ".json")
				result = append(result, fmt.Sprintf("%s/%s", p, name))
			}
		}
	}
	// Merge user-installed templates from ~/.monoagent/actions/
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
					name := strings.TrimSuffix(e.Name(), ".json")
					candidate := fmt.Sprintf("%s/%s", p, name)
					// Deduplicate: skip if embedded FS already provided this one.
					duplicate := false
					for _, existing := range result {
						if existing == candidate {
							duplicate = true
							break
						}
					}
					if !duplicate {
						result = append(result, candidate)
					}
				}
			}
		}
	}

	return result, nil
}

// Invalidate removes a cached action definition, forcing the next Load call
// for that key to re-read from the embedded filesystem.
func (l *ActionLoader) Invalidate(platform, actionType string) {
	key := fmt.Sprintf("%s/%s", strings.ToLower(platform), strings.ToLower(actionType))
	l.cache.Delete(key)
}

// InvalidateAll clears the entire cache.
func (l *ActionLoader) InvalidateAll() {
	l.cache.Range(func(key, _ interface{}) bool {
		l.cache.Delete(key)
		return true
	})
}

var (
	defSourceMu sync.RWMutex
	defSource   DefSource
)

// SetDefSource installs the definition source used by every loader (the
// automation registry calls this at startup). nil restores the legacy
// embedded-seed + ~/.monoagent/actions behaviour. Clears the cache.
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
