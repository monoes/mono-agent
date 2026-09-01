// Package i18n is a minimal, dependency-free translation lookup for the CLI.
//
// Locale files live in locales/<lang>.json as flat key->string maps and are
// embedded into the binary. The active locale is resolved once at process
// start (see Detect) because Cobra command Short/Long/Example strings are
// built once, before flags are parsed — see docs/i18n.md for why this
// package resolves the locale from argv/env rather than a parsed flag.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

const (
	// DefaultLocale is used when no locale is requested, and as the
	// fallback for keys missing from a non-English locale file.
	DefaultLocale = "en"

	// EnvVar is the environment variable consulted by Detect.
	EnvVar = "MONOAGENT_LANG"
)

var (
	mu      sync.RWMutex
	current = DefaultLocale
	cache   = map[string]map[string]string{}
)

// Locales returns the set of embedded locale codes (derived from the
// filenames under locales/), e.g. ["en", "es"].
func Locales() []string {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			out = append(out, strings.TrimSuffix(name, ".json"))
		}
	}
	return out
}

func loadLocale(lang string) (map[string]string, error) {
	mu.RLock()
	m, ok := cache[lang]
	mu.RUnlock()
	if ok {
		return m, nil
	}

	data, err := localeFS.ReadFile("locales/" + lang + ".json")
	if err != nil {
		return nil, err
	}
	parsed := map[string]string{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}

	mu.Lock()
	cache[lang] = parsed
	mu.Unlock()
	return parsed, nil
}

// SetLocale sets the active locale used by T. An unknown or empty locale
// silently falls back to DefaultLocale — a bad --lang value should never
// crash the CLI, just print English.
func SetLocale(lang string) {
	if lang == "" {
		lang = DefaultLocale
	}
	if _, err := loadLocale(lang); err != nil {
		lang = DefaultLocale
	}
	mu.Lock()
	current = lang
	mu.Unlock()
}

// CurrentLocale returns the active locale code.
func CurrentLocale() string {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Detect resolves the requested locale from argv (looking for --lang=xx or
// --lang xx) and falls back to the MONOAGENT_LANG environment variable, then
// DefaultLocale. It does not consult a parsed Cobra flag because the locale
// must be known before command trees (whose Short/Long strings call T) are
// constructed — see the package doc comment.
func Detect(args []string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, "--lang="); ok {
			return v
		}
		if a == "--lang" && i+1 < len(args) {
			return args[i+1]
		}
	}
	if v := os.Getenv(EnvVar); v != "" {
		return v
	}
	return DefaultLocale
}

// T looks up key in the active locale. Missing keys fall back to the
// English string, then to the raw key itself so a typo or an untranslated
// string never produces empty output. args are applied with fmt.Sprintf
// when present, so locale strings may contain %s/%d verbs.
func T(key string, args ...any) string {
	lang := CurrentLocale()

	if s, ok := lookup(lang, key); ok {
		return format(s, args)
	}
	if lang != DefaultLocale {
		if s, ok := lookup(DefaultLocale, key); ok {
			return format(s, args)
		}
	}
	return key
}

func lookup(lang, key string) (string, bool) {
	m, err := loadLocale(lang)
	if err != nil {
		return "", false
	}
	s, ok := m[key]
	return s, ok
}

func format(s string, args []any) string {
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}
