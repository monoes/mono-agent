package apiconfig

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// Where the effective value of a setting comes from, strongest first.
const (
	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceSaved   = "saved"
	SourceDefault = "default"
)

// Flags are what the command line of `httpapi` or `daemon` gave. A field that is empty (a
// number that is 0) was not given.
type Flags struct {
	V1Addr, Confinement, ContextConfinement, AutoConfinement string
	MaxConcurrent                                            int
}

func (f Flags) text(key string) string {
	switch key {
	case KeyV1Addr:
		return f.V1Addr
	case KeyConfinement:
		return f.Confinement
	case KeyContextConfinement:
		return f.ContextConfinement
	case KeyAutoConfinement:
		return f.AutoConfinement
	case KeyMaxConcurrent:
		if f.MaxConcurrent != 0 {
			return strconv.Itoa(f.MaxConcurrent)
		}
	}
	return ""
}

// Resolved is the effective value of one setting: its text in the canonical spelling ("" where
// there is no value) and the Source it came from.
type Resolved struct {
	Key, Text, Source string
}

func specFor(key string) (Spec, bool) {
	for _, sp := range Specs() {
		if sp.Key == key {
			return sp, true
		}
	}
	return Spec{}, false
}

func keyOfEnv(name string) (string, bool) {
	for _, sp := range Specs() {
		if sp.Env == name {
			return sp.Key, true
		}
	}
	return "", false
}

func isTLS(key string) bool { return key == KeyTLSCertFile || key == KeyTLSKeyFile }

// layer is the rule: flag, then the environment, then the saved value, then the default. It
// returns the text as the winning layer has it and which layer that is. An empty variable is
// an unset one. The two TLS files are a pair: if either variable is set the pair is the
// environment's, even with one file missing (the server then says so), else the saved pair.
func layer(key, flag string, getenv func(string) string, saved Settings) (raw, source string) {
	sp, ok := specFor(key)
	if !ok {
		return "", SourceDefault
	}
	if flag != "" {
		return flag, SourceFlag
	}
	if isTLS(key) {
		cert, _ := specFor(KeyTLSCertFile)
		keyFile, _ := specFor(KeyTLSKeyFile)
		switch {
		case getenv(cert.Env) != "" || getenv(keyFile.Env) != "":
			return getenv(sp.Env), SourceEnv
		case saved.TLSCertFile != "" || saved.TLSKeyFile != "":
			return saved.Get(key), SourceSaved
		}
		return sp.Default, SourceDefault
	}
	if v := getenv(sp.Env); v != "" {
		return v, SourceEnv
	}
	if v := saved.Get(key); v != "" {
		return v, SourceSaved
	}
	return sp.Default, SourceDefault
}

// ResolveKey is the effective value of one setting and where it comes from: the flag if one
// was given (flag is its text), else the environment variable if it is not empty, else the
// saved value, else the default. Its text is in the canonical spelling; a value that fails its
// rule is reported as it is.
func ResolveKey(key, flag string, getenv func(string) string, saved Settings) Resolved {
	raw, source := layer(key, flag, getenv, saved)
	text := raw
	if raw != "" {
		if canon, err := Canonical(key, raw); err == nil {
			text = canon
		}
	}
	return Resolved{Key: key, Text: text, Source: source}
}

// ResolveAll is ResolveKey for the ten settings, in the order of the documents. f is what the
// command line gave.
func ResolveAll(f Flags, getenv func(string) string, saved Settings) []Resolved {
	keys := Keys()
	out := make([]Resolved, len(keys))
	for i, key := range keys {
		out[i] = ResolveKey(key, f.text(key), getenv, saved)
	}
	return out
}

// Overlay is getenv with the saved layer under it: for the variables of the API it answers the
// environment's value when it is not empty and the saved value otherwise, and "" when neither
// has one, which leaves each reader its own default. Every other name goes to getenv. The
// readers of the environment (the server, `api models`, `api status`, the MCP tool
// api_models_list) read through it, so that they agree with a server started now; the flags
// stay their explicit arguments and win over both.
func Overlay(saved Settings, getenv func(string) string) func(string) string {
	return func(name string) string {
		key, ok := keyOfEnv(name)
		if !ok {
			return getenv(name)
		}
		raw, source := layer(key, "", getenv, saved)
		if source == SourceDefault {
			return ""
		}
		return raw
	}
}

// EnvWithSaved loads the saved settings, checks them and returns Overlay over getenv. A server
// does not start on a setting that fails its rule, so neither do the readers that stand for a
// server started now: the error names each setting and says how to fix it. A saved document
// that cannot be read is the error of Load.
func EnvWithSaved(ctx context.Context, db *sql.DB, getenv func(string) string) (func(string) string, error) {
	saved, err := Load(ctx, db)
	if err != nil {
		return nil, err
	}
	if problems := Validate(saved); len(problems) > 0 {
		return nil, fmt.Errorf("saved API settings: %w; change them with `monoagentcli api config set`, or remove one with `monoagentcli api config unset <setting>`",
			&ValidationError{Problems: problems})
	}
	return Overlay(saved, getenv), nil
}
