package main

// `api models` and `api status` stand for a server started now, so they read what it reads:
// flag, environment, saved, default. A running daemon is still believed about what it
// applies.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"os"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/storage"
)

// saveAt saves settings in the database at dbPath, the one the commands of the test open.
func saveAt(t *testing.T, dbPath string, words ...string) {
	t.Helper()
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := apiconfig.Update(context.Background(), db.DB, func(s *apiconfig.Settings) error {
		for _, w := range words {
			key, value, _ := strings.Cut(w, "=")
			if err := s.Set(key, value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func clearAPIEnv(t *testing.T) {
	t.Helper()
	for _, sp := range apiconfig.Specs() {
		t.Setenv(sp.Env, "")
	}
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "")
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "none.json"))
}

func TestAPIModelsReadsTheSavedSettings(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	clearAPIEnv(t)
	saveAt(t, db, "confinement=sandboxed", "context_confinement=sandboxed", "auto_confinement=sandboxed", "image_runtimes=none", "tool_runtimes=none")

	out, _, err := runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	m := decodeModels(t, out)
	if m.Policy.Confinement != "sandboxed" || m.Policy.ContextConfinement != "sandboxed" || m.Policy.AutoConfinement != "sandboxed" {
		t.Errorf("the saved classes: %+v", m.Policy)
	}
	for id, caps := range capabilitiesByID(m) {
		if !slices.Equal(caps, []string{"text"}) {
			t.Errorf("%s: capabilities %v, want text alone: images and tools are saved as none", id, caps)
		}
	}
	if allowed := allowedByID(m); allowed["claude/default"] != true || allowed["codex/default"] != true || allowed["antigravity/default"] != false {
		t.Errorf("a saved confinement of sandboxed serves claude and codex and not antigravity: %v", allowed)
	}

	// The environment is over the saved value, and the flag over both.
	t.Setenv("MONOAGENT_API_CONFINEMENT", "chat-only")
	t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", "codex")
	out, _, err = runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	m = decodeModels(t, out)
	if m.Policy.Confinement != "chat-only" || !slices.Contains(capabilitiesByID(m)["codex/default"], "image") {
		t.Errorf("the environment should win over the saved settings: %+v %v", m.Policy, capabilitiesByID(m)["codex/default"])
	}
	out, _, err = runAPI(t, db, "default", true, "models", "--confinement", "any")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeModels(t, out).Policy.Confinement; got != "any" {
		t.Errorf("--confinement any: %s", got)
	}
}

func TestAPIModelsRefusesInvalidSavedSettingsByName(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	clearAPIEnv(t)
	saveAt(t, db, "confinement=everything-supersecret")
	_, _, err := runAPI(t, db, "default", true, "models")
	if exitCodeFor(err) != 3 || err == nil || !strings.Contains(err.Error(), "confinement must be") || strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("exit %d, %v: want 3, naming the setting and not repeating its value", exitCodeFor(err), err)
	}
}

func TestAPIStatusReadsTheSavedSettingsWithoutADaemon(t *testing.T) {
	db := newAPITestDB(t)
	clearAPIEnv(t)
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", apiServer(t, true))
	saveAt(t, db, "v1_addr=127.0.0.1:1", "confinement=sandboxed", "context_confinement=any", "auto_confinement=sandboxed")

	status := func() apiStatusJSON {
		t.Helper()
		out, _, err := runAPI(t, db, "default", true, "status")
		if err != nil {
			t.Fatal(err)
		}
		return decodeStatus(t, out)
	}
	st := status()
	if len(st.Listeners) != 2 || st.Listeners[1].Name != "v1" || st.Listeners[1].Addr != "127.0.0.1:1" {
		t.Fatalf("the saved dedicated listener should be listed: %+v", st.Listeners)
	}
	main := st.Listeners[0]
	if main.Confinement != "sandboxed" || main.ConfinementSource != "environment" || main.ContextConfinement != "sandboxed" || main.AutoConfinement != "sandboxed" {
		t.Errorf("main listener: %+v", main)
	}
	if v1 := st.Listeners[1]; v1.Confinement != "sandboxed" || v1.ConfinementSource != "environment" {
		t.Errorf("v1 listener: %+v", v1)
	}

	// The environment is over the saved value.
	t.Setenv("MONOAGENT_API_CONFINEMENT", "chat-only")
	t.Setenv("MONOAGENT_API_V1_ADDR", "127.0.0.1:2")
	st = status()
	if st.Listeners[0].Confinement != "chat-only" || st.Listeners[1].Addr != "127.0.0.1:2" {
		t.Errorf("the environment should win over the saved settings: %+v", st.Listeners)
	}
}

// What a running daemon reports is what it applies, saved settings or not: they take effect
// at its next start.
func TestAPIStatusBelievesTheRunningDaemonOverTheSavedSettings(t *testing.T) {
	db := newAPITestDB(t)
	clearAPIEnv(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	addr := apiServer(t, true)
	saveAt(t, db, "confinement=any", "context_confinement=any", "auto_confinement=any")
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), APIAddr: addr, APIConfinement: "chat-only", ContextConfinement: "chat-only", AutoConfinement: "chat-only"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	l := decodeStatus(t, out).Listeners[0]
	if l.Confinement != "chat-only" || l.ConfinementSource != "daemon" || l.ContextConfinement != "chat-only" || l.AutoConfinement != "chat-only" {
		t.Errorf("the daemon applies chat-only: %+v", l)
	}
}

func TestAPIStatusRefusesInvalidSavedSettingsByName(t *testing.T) {
	db := newAPITestDB(t)
	clearAPIEnv(t)
	saveAt(t, db, "max_concurrent=0")
	_, _, err := runAPI(t, db, "default", true, "status")
	if exitCodeFor(err) != 3 || err == nil || !strings.Contains(err.Error(), "max_concurrent must be") {
		t.Fatalf("exit %d, %v: want 3, naming the setting", exitCodeFor(err), err)
	}
}
