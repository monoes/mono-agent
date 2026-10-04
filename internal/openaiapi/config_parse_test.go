package openaiapi

import (
	"strings"
	"testing"
	"time"
)

// The two limits that are plain text in MONOAGENT_API_MAX_CONCURRENT and
// MONOAGENT_API_TURN_TIMEOUT have parsers of their own, so that the saved settings of
// the server (internal/apiconfig) are held to the rule the environment is held to.

func TestParseMaxConcurrent(t *testing.T) {
	for in, want := range map[string]int{"1": 1, "4": 4, "64": 64, "+5": 5, "007": 7} {
		if got, err := ParseMaxConcurrent(in); err != nil || got != want {
			t.Errorf("ParseMaxConcurrent(%q) = %d, %v, want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "65", "-1", "abc", " 8", "8 ", "1e1", "4.0", "99999999999999999999"} {
		if got, err := ParseMaxConcurrent(in); err == nil {
			t.Errorf("ParseMaxConcurrent(%q) = %d, want an error", in, got)
		}
	}
}

func TestParseTurnTimeout(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"10s": 10 * time.Second, "15m": 15 * time.Minute, "90s": 90 * time.Second, "1h30m": 90 * time.Minute, "10m": 10 * time.Minute,
	} {
		if got, err := ParseTurnTimeout(in); err != nil || got != want {
			t.Errorf("ParseTurnTimeout(%q) = %v, %v, want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "9s", "9.9s", "0", "-5m", " 15m", "15m ", "15", "abc"} {
		if got, err := ParseTurnTimeout(in); err == nil {
			t.Errorf("ParseTurnTimeout(%q) = %v, want an error", in, got)
		}
	}
}

func TestTheLimitsHaveExportedDefaults(t *testing.T) {
	if DefaultMaxConcurrent != 4 || DefaultTurnTimeout != 10*time.Minute || MinTurnTimeout != 10*time.Second {
		t.Errorf("defaults: %d, %v, %v", DefaultMaxConcurrent, DefaultTurnTimeout, MinTurnTimeout)
	}
	c, err := Config{ScratchRoot: t.TempDir()}.withDefaults() // a root of its own: the default one is under HOME
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxConcurrent != DefaultMaxConcurrent || c.TurnTimeout != DefaultTurnTimeout {
		t.Errorf("withDefaults gives %d and %v", c.MaxConcurrent, c.TurnTimeout)
	}
}

// What a person who sets a bad variable reads did not change when the rule moved into
// the parsers: it names the variable, says the rule and echoes the value.
func TestConfigFromEnvMessagesNameTheVariable(t *testing.T) {
	for env, want := range map[string]string{
		"MONOAGENT_API_MAX_CONCURRENT=99":  `MONOAGENT_API_MAX_CONCURRENT must be an integer from 1 to 64, got "99"`,
		"MONOAGENT_API_MAX_CONCURRENT=abc": `MONOAGENT_API_MAX_CONCURRENT must be an integer from 1 to 64, got "abc"`,
		"MONOAGENT_API_TURN_TIMEOUT=5s":    `MONOAGENT_API_TURN_TIMEOUT must be a duration of at least 10s, such as 15m, got "5s"`,
		"MONOAGENT_API_TURN_TIMEOUT=soon":  `MONOAGENT_API_TURN_TIMEOUT must be a duration of at least 10s, such as 15m, got "soon"`,
	} {
		name, value, _ := strings.Cut(env, "=")
		_, err := ConfigFromEnv(func(k string) string {
			if k == name {
				return value
			}
			return ""
		})
		if err == nil || err.Error() != want {
			t.Errorf("%s: error %v, want %q", env, err, want)
		}
	}
}
