package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// agentNotSetupCode / agentNotSetupMarker name the CLI's classification of
// "the AI agent is not set up" (see monomind.AgentNotSetupCode): a code on
// JSON errors, a marker at the end of a stored run error.
const (
	agentNotSetupCode   = monomind.AgentNotSetupCode
	agentNotSetupMarker = monomind.AgentNotSetupMarker
)

func aiError(err error) string {
	code := ""
	var ce *codedError
	if errors.As(err, &ce) {
		code = ce.code
	} else if monomind.IsAgentNotSetup(err) {
		code = agentNotSetupCode
	}
	if code != "" {
		b, _ := json.Marshal(map[string]string{"error": err.Error(), "code": code})
		return string(b)
	}
	return fmt.Sprintf(`{"error":%q}`, err.Error())
}

// ─────────────────────────────────────────────────────────────────────────────
// Agent runtimes (monomind delegation — Agent Exec Protocol)
//
// Chat turns themselves run through the chat supervisor (app_chat.go), which
// spawns `monoagentcli chat`.
// ─────────────────────────────────────────────────────────────────────────────

// ScanAgentRuntimes detects installed agent runtimes through the monomind
// engine. Returns JSON {v, agents} or {error} with the actionable install
// hint when monomind itself is missing.
func (a *App) ScanAgentRuntimes() string {
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	res, err := monomind.Scan(ctx)
	if err != nil {
		return aiError(err)
	}
	b, _ := json.Marshal(res)
	return string(b)
}

// GetAgentRuntimeModels returns the models selectable for one agent
// runtime's --model flag, as JSON []monomind.RuntimeModel (or {error}).
// binary is the runtime's resolved path from a prior ScanAgentRuntimes call
// (ScanEntry.binary) — required for antigravity/codex, which discover their
// own model catalog by shelling out to themselves; ignored for claude,
// which has no such command and falls back to a curated list. Returns "[]"
// (not an error) for a runtime ListModels doesn't recognize, so the
// frontend can fall back to a plain free-text model field either way.
func (a *App) GetAgentRuntimeModels(runtimeID, binary string) string {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	models, err := monomind.ListModels(ctx, runtimeID, binary)
	if err != nil {
		return aiError(err)
	}
	if models == nil {
		models = []monomind.RuntimeModel{}
	}
	b, _ := json.Marshal(models)
	return string(b)
}

// chatLogWriter sinks the chat CLI's stderr into the UI log pane.
func (a *App) chatLogWriter() *chatLogPipe {
	return &chatLogPipe{app: a}
}

type chatLogPipe struct {
	app *App
	buf []byte
}

func (p *chatLogPipe) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := indexOfByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(p.buf[:i]))
		p.buf = p.buf[i+1:]
		if line != "" {
			p.app.emitLog("AI", "INFO", line)
		}
	}
	return len(b), nil
}

func indexOfByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
