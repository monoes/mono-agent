// Package apiconfig owns the saved settings of the OpenAI-compatible API's server and
// everything the surfaces share about them: the checks, the order of precedence
// (flag, environment, saved, default), the exposure gate, the state of each setting
// against the running daemon and the documents `monoagentcli api config` and
// `api status` print. The CLI, the MCP tools and the desktop app (through the CLI)
// all read and change settings through it, so they agree.
//
// The rules of what a value means are internal/openaiapi's: the flags, the
// environment and the saved layer are held to the same parsers.
package apiconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/openaiapi"
)

// The ten settings, by the key they have in every document and in the saved row.
const (
	KeyV1Addr             = "v1_addr"
	KeyTLSCertFile        = "tls_cert_file"
	KeyTLSKeyFile         = "tls_key_file"
	KeyConfinement        = "confinement"
	KeyContextConfinement = "context_confinement"
	KeyAutoConfinement    = "auto_confinement"
	KeyMaxConcurrent      = "max_concurrent"
	KeyTurnTimeout        = "turn_timeout"
	KeyImageRuntimes      = "image_runtimes"
	KeyToolRuntimes       = "tool_runtimes"
)

// Spec describes one setting.
type Spec struct {
	Key string
	// ServerFlag is the flag `httpapi` and `daemon` have for it, "" when it has none and the
	// environment is the only way to give it at a start.
	ServerFlag string
	// Env is the environment variable that names it.
	Env string
	// Default is the canonical text of what the server uses when nothing sets it: "" where
	// there is no value (no dedicated listener, no certificate, a policy that depends on the
	// listener).
	Default string
}

// Specs lists the settings in the order every document has them.
func Specs() []Spec {
	return []Spec{
		{KeyV1Addr, "--v1-addr", "MONOAGENT_API_V1_ADDR", ""},
		{KeyTLSCertFile, "", "MONOAGENT_API_TLS_CERT", ""},
		{KeyTLSKeyFile, "", "MONOAGENT_API_TLS_KEY", ""},
		{KeyConfinement, "--confinement", "MONOAGENT_API_CONFINEMENT", ""},
		{KeyContextConfinement, "--context-confinement", "MONOAGENT_API_CONTEXT_CONFINEMENT", defaultClass},
		{KeyAutoConfinement, "--auto-confinement", "MONOAGENT_API_AUTO_CONFINEMENT", defaultClass},
		{KeyMaxConcurrent, "--max-concurrent", "MONOAGENT_API_MAX_CONCURRENT", strconv.Itoa(openaiapi.DefaultMaxConcurrent)},
		{KeyTurnTimeout, "", "MONOAGENT_API_TURN_TIMEOUT", formatDuration(openaiapi.DefaultTurnTimeout)},
		{KeyImageRuntimes, "", "MONOAGENT_API_IMAGE_RUNTIMES", defaultRuntimes(openaiapi.ParseImageRuntimes)},
		{KeyToolRuntimes, "", "MONOAGENT_API_TOOL_RUNTIMES", defaultRuntimes(openaiapi.ParseToolRuntimes)},
	}
}

// defaultClass is what a key created with --context and the auto model are held to when
// the operator raised nothing.
const defaultClass = "chat-only"

// Keys lists the keys of the settings in the order every document has them.
func Keys() []string {
	specs := Specs()
	keys := make([]string, len(specs))
	for i, sp := range specs {
		keys[i] = sp.Key
	}
	return keys
}

// LookupKey says which setting a name means: its key, or the spelling of its flag with
// dashes (v1-addr). It reports whether there is such a setting.
func LookupKey(name string) (key string, ok bool) {
	key = strings.ReplaceAll(name, "-", "_")
	for _, k := range Keys() {
		if k == key {
			return k, true
		}
	}
	return "", false
}

// Settings are the saved values, each the text of the setting in the syntax of its
// environment variable ("" = not saved).
type Settings struct {
	V1Addr, TLSCertFile, TLSKeyFile, Confinement, ContextConfinement, AutoConfinement,
	MaxConcurrent, TurnTimeout, ImageRuntimes, ToolRuntimes string

	// extra holds the fields of the stored document that this binary does not know, so that
	// a write puts them back as they were.
	extra map[string]json.RawMessage
}

// Defaults are the values the server uses when nothing is set, as text ("" where a setting
// has no value to default to).
func Defaults() Settings {
	var s Settings
	for _, sp := range Specs() {
		_ = s.Set(sp.Key, sp.Default)
	}
	return s
}

func (s *Settings) field(key string) *string {
	switch key {
	case KeyV1Addr:
		return &s.V1Addr
	case KeyTLSCertFile:
		return &s.TLSCertFile
	case KeyTLSKeyFile:
		return &s.TLSKeyFile
	case KeyConfinement:
		return &s.Confinement
	case KeyContextConfinement:
		return &s.ContextConfinement
	case KeyAutoConfinement:
		return &s.AutoConfinement
	case KeyMaxConcurrent:
		return &s.MaxConcurrent
	case KeyTurnTimeout:
		return &s.TurnTimeout
	case KeyImageRuntimes:
		return &s.ImageRuntimes
	case KeyToolRuntimes:
		return &s.ToolRuntimes
	}
	return nil
}

// Get is the saved text of a setting, "" when it is not saved (or the key is unknown).
func (s Settings) Get(key string) string {
	if f := s.field(key); f != nil {
		return *f
	}
	return ""
}

// Set stores the text of a setting without checking it (Validate does). It fails only for a
// key that is not a setting, and then says which the settings are and not what was given.
func (s *Settings) Set(key, text string) error {
	f := s.field(key)
	if f == nil {
		return errUnknownKey()
	}
	*f = text
	return nil
}

// Unset removes the saved text of a setting.
func (s *Settings) Unset(key string) {
	if f := s.field(key); f != nil {
		*f = ""
	}
}

// IsEmpty reports whether no setting is saved.
func (s Settings) IsEmpty() bool {
	for _, k := range Keys() {
		if s.Get(k) != "" {
			return false
		}
	}
	return true
}

func errUnknownKey() error {
	return fmt.Errorf("not a setting: the settings are %s", strings.Join(Keys(), ", "))
}

// Problem is one thing wrong with a saved value: the setting and the rule it breaks. Key is
// "" for a problem of the document as a whole.
type Problem struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

// Validate checks every saved value with the parsers the flags and the environment use, on
// the text as it was typed, and the pair of TLS files as a whole. A setting that is not
// saved is skipped. The messages name the setting and the rule and repeat what was typed
// only for an address.
func Validate(s Settings) []Problem {
	var out []Problem
	for _, key := range Keys() {
		text := s.Get(key)
		if text == "" {
			continue
		}
		if _, err := Canonical(key, text); err != nil {
			out = append(out, Problem{Key: key, Message: err.Error()})
		}
	}
	// The pair is a pair: one file alone would be an error in the environment too.
	if hasCert, hasKey := s.TLSCertFile != "", s.TLSKeyFile != ""; hasCert != hasKey {
		key := KeyTLSKeyFile
		if hasCert {
			key = KeyTLSCertFile
		}
		out = append(out, Problem{Key: key, Message: "tls_cert_file and tls_key_file must be set together"})
	}
	return out
}

// Canonical is the spelling in which the text of a setting is stored, for text that the
// setting accepts, and otherwise why it does not (an error that names the setting and the
// rule). The rules are the ones of the flags and the environment: a padded duration or a
// padded class is refused here as it is there, and what is accepted is stored without the
// sign of a number, with a runtime list in lower case, `agy` spelled out and repeats
// dropped, and with a duration as short as it can be written.
func Canonical(key, text string) (string, error) {
	switch key {
	case KeyV1Addr:
		if err := ValidListenAddr(text); err != nil {
			return "", fmt.Errorf("v1_addr must be host:port, such as 127.0.0.1:9443 or :9443: %v", err)
		}
		return text, nil
	case KeyTLSCertFile, KeyTLSKeyFile:
		return text, nil
	case KeyConfinement, KeyContextConfinement, KeyAutoConfinement:
		if _, err := openaiapi.ParsePolicy(text); err != nil {
			return "", fmt.Errorf("%s must be chat-only, sandboxed or any", key)
		}
		return text, nil
	case KeyMaxConcurrent:
		n, err := openaiapi.ParseMaxConcurrent(text)
		if err != nil {
			return "", fmt.Errorf("max_concurrent %v", err)
		}
		return strconv.Itoa(n), nil
	case KeyTurnTimeout:
		d, err := openaiapi.ParseTurnTimeout(text)
		if err != nil {
			return "", fmt.Errorf("turn_timeout %v", err)
		}
		return formatDuration(d), nil
	case KeyImageRuntimes:
		list, err := openaiapi.ParseImageRuntimes(text)
		if err != nil {
			return "", fmt.Errorf("image_runtimes must be a comma-separated list of runtime ids, such as codex,antigravity, or none alone to switch image generation off")
		}
		return runtimeList(list), nil
	case KeyToolRuntimes:
		list, err := openaiapi.ParseToolRuntimes(text)
		if err != nil {
			return "", fmt.Errorf("tool_runtimes must be a comma-separated list of runtime ids, such as claude,codex, or none alone to switch tool calling off")
		}
		return runtimeList(list), nil
	}
	return "", errUnknownKey()
}

// runtimeList is a parsed runtime list as text: "none" for the list with nothing in it.
func runtimeList(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ",")
}

// defaultRuntimes is the default list of a parser, as text.
func defaultRuntimes(parse func(string) ([]string, error)) string {
	list, _ := parse("")
	return runtimeList(list)
}

// formatDuration writes a duration as Go does, without the zero units at the end that Go
// adds (15m, not 15m0s).
func formatDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

// ValidListenAddr checks the shape of a listen address: host:port with a numeric port. The
// host may be empty, as in :9443.
func ValidListenAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("%q is not a port number", port)
	}
	return nil
}
