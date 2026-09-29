package main

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/dynorg"
	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/monomind"
)

// noticeOrgUnavailable is journaled when a dynamic-org conversation's turn
// has to run solo, with the reason.
const noticeOrgUnavailable = "org.unavailable"

// orgUnavailableReason says why this turn's lead can't get org tools, or
// "" when it can: monomind needs caller tools alongside full access
// (agent-exec-full-access-tools), on this runtime.
func orgUnavailableReason(st coderStatus, runtime string) string {
	if !st.caps.Has(monomind.CapAgentExecFullAccessTools) {
		return "this monomind can't give a full-access agent caller tools (needs monomind 2.19 or newer), so the agent works alone this turn"
	}
	if st.scan == nil {
		return "the runtime scan failed, so the agent works alone this turn"
	}
	e := st.scan.Find(runtime)
	if e == nil || !e.CallerToolsWithFullAccess {
		return runtime + " can't take caller tools with full access, so the agent works alone this turn"
	}
	return ""
}

// orgRoster is the models a dynamic org may staff: the ready (and stale)
// entries of the validated roster whose runtime is installed and runs with
// full or read access. With no roster it is the lead's own model alone.
func orgRoster(ctx context.Context, db *sql.DB, st coderStatus, lead dynorg.Model) []dynorg.Model {
	results, err := agentroster.List(ctx, db)
	if err != nil || len(results) == 0 {
		return []dynorg.Model{lead}
	}
	readCap := st.caps.Has(monomind.CapAgentExecAccessRead)
	var models []dynorg.Model
	for _, rr := range agentroster.Build(results, st.scan, time.Now(), agentroster.DefaultMaxAge) {
		var e *monomind.ScanEntry
		if st.scan != nil {
			e = st.scan.Find(rr.Runtime)
		}
		if e == nil || !e.Installed {
			continue
		}
		read := readCap && slices.Contains(e.AccessModes, monomind.AccessRead)
		if !e.FullAccess && !read {
			continue
		}
		for _, m := range rr.Models {
			if m.State != agentroster.StateReady && m.State != agentroster.StateStale {
				continue
			}
			model := m.Model
			if model == agentroster.DefaultModel {
				model = ""
			}
			models = append(models, dynorg.Model{
				Runtime: rr.Runtime, Model: model, Label: m.Label, Efforts: m.EffortLevels,
				FullAccess: e.FullAccess, Read: read, Resume: e.Resume,
				CostUSD: m.CostUSD, LatencyMs: m.LatencyMs, Stale: m.State == agentroster.StateStale,
			})
		}
	}
	if len(models) == 0 {
		return []dynorg.Model{lead}
	}
	// The lead's own model first, then ready before stale.
	slices.SortStableFunc(models, func(a, b dynorg.Model) int {
		switch {
		case a.Key() == lead.Key() && b.Key() != lead.Key():
			return -1
		case b.Key() == lead.Key() && a.Key() != lead.Key():
			return 1
		case a.Stale != b.Stale && !a.Stale:
			return -1
		case a.Stale != b.Stale:
			return 1
		}
		return 0
	})
	return models
}

// startDynamicOrg wires a conductor into a dynamic-org coder turn: the lead
// gets the org tools and the org part of its system prompt. It returns a
// close func that ends every worker (call it before the turn finishes), or
// nil with the journal told why the turn runs solo.
func startDynamicOrg(ctx context.Context, cfg *globalConfig, journal *turnJournal, settings coderSettings, st coderStatus, rt monomind.CoderRuntime, t coderTurn, opts *monomind.ExecOptions) func() {
	if reason := orgUnavailableReason(st, rt.ID); reason != "" {
		journal.notice(noticeOrgUnavailable, reason, chatevents.SeverityWarning)
		return nil
	}
	db, err := initDB(cfg)
	if err != nil {
		journal.notice(noticeOrgUnavailable, "the agent works alone this turn: "+err.Error(), chatevents.SeverityWarning)
		return nil
	}
	lead := dynorg.Model{Runtime: rt.ID, Model: t.model, FullAccess: true, Resume: rt.Resume}
	if e := st.scan.Find(rt.ID); e != nil {
		lead.Read = st.caps.Has(monomind.CapAgentExecAccessRead) && slices.Contains(e.AccessModes, monomind.AccessRead)
	}
	lib := &dynorg.MonomindLibrary{Bin: opts.Bin, Cwd: t.cwd}
	staffer := &dynorg.Staffer{
		Roster: orgRoster(ctx, db.DB, st, lead), Lead: lead, ModelPicker: settings.OrgModelPicker,
		Picker: lib, Library: lib,
	}
	if client, err := jevconf.NewClient(ctx, db.DB, cfg.ProfileID, "", "", jevconf.DynamicOrg); err == nil {
		staffer.Chooser = dynorg.JevChooser{Client: client}
	}
	limits := dynorg.Limits{MaxAgents: settings.OrgMaxAgents, MaxConcurrent: settings.OrgMaxConcurrent, BudgetUSD: settings.OrgBudgetUSD}
	base := monomind.ExecOptions{
		Bin: opts.Bin, Settings: monomind.CoderSettings, MaxTurns: settings.MaxTurns, Timeout: settings.timeout(),
		EffortFlag: opts.EffortFlag,
	}
	cond := dynorg.New(ctx, dynorg.Config{
		Cwd: t.cwd, Limits: limits, Staffer: staffer, Base: base,
		ReadAccess: st.caps.Has(monomind.CapAgentExecAccessRead),
		Emit:       journal,
		Outcome: func(runtime, model, status, detail string, at time.Time) {
			_ = agentroster.RecordOutcome(context.WithoutCancel(ctx), db.DB, runtime, model, status, detail, at)
		},
	})
	opts.Tools = dynorg.ToolSpecs()
	opts.OnToolCall = cond.Handle
	opts.ToolTimeout = dynorg.ToolTimeout
	opts.SystemPrompt += dynorg.LeadPrompt(limits)
	return func() {
		cond.Close()
		db.Close()
	}
}

// Emit implements dynorg.Emitter: a worker's event, journaled in the
// lead's turn. Events after the turn finished are dropped.
func (j *turnJournal) Emit(typ chatevents.EventType, payload any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.finished {
		return
	}
	_ = j.appendLocked(typ, payload)
}
