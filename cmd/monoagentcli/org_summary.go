package main

import (
	"context"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// orgSummaryRow is one org in `org summary`. NeedsYou is null in --fast
// mode and when it could not be computed (NeedsYouError says why).
type orgSummaryRow struct {
	Name          string `json:"name"`
	Running       bool   `json:"running"`
	Level         string `json:"level"`
	Paused        bool   `json:"paused"`
	Queued        int    `json:"queued"`
	NeedsYou      *int   `json:"needs_you"`
	NeedsYouError string `json:"needs_you_error,omitempty"`
}

// perOrgNeedsYouTimeout bounds the monomind round-trips for one org.
const perOrgNeedsYouTimeout = 10 * time.Second

func newOrgSummaryCmd(env *orgEnv) *cobra.Command {
	var fast bool
	c := &cobra.Command{
		Use:   "summary",
		Short: "One row per org: running, autonomy level, queued messages, items waiting for you",
		Long: "Cross-org roll-up for the dashboard. --fast reads local files and the database only " +
			"(no monomind process) and leaves needs_you null. Without it, needs_you is computed " +
			"for every org in parallel, each bounded to 10 seconds.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := env.Root()
			names, err := orgdesign.ListOrgNames(root)
			if err != nil {
				return err
			}
			hb, serveLive := monomind.ReadServeHeartbeat(root)
			running := map[string]bool{}
			if hb != nil && serveLive {
				for _, n := range hb.Running {
					running[n] = true
				}
			}
			// Autonomy rows and needs-you need the profile database; without
			// it the local facts (running, queued) are still reported.
			db, profileID, _, dbErr := env.Profile()
			var svc *orgdecide.Service
			if dbErr == nil {
				svc = orgdecide.NewService(db.DB, nil)
			}
			now := time.Now()
			rows := make([]orgSummaryRow, len(names))
			var wg sync.WaitGroup
			for i, name := range names {
				row := orgSummaryRow{Name: name, Running: running[name], Level: orgdesign.LevelManual}
				if svc != nil {
					if a, err := svc.Store.Get(cmd.Context(), profileID, name); err == nil && a != nil {
						row.Level = a.EffectiveLevel(now)
						row.Paused = a.PausedUntil != nil && a.PausedUntil.After(now)
					}
				}
				if msgs, _, err := orgbridge.ReadQueued(root, name); err == nil {
					row.Queued = len(msgs)
				}
				rows[i] = row
				if fast {
					continue
				}
				if dbErr != nil {
					rows[i].NeedsYouError = dbErr.Error()
					continue
				}
				wg.Add(1)
				go func(i int, name string) {
					defer wg.Done()
					ctx, cancel := context.WithTimeout(cmd.Context(), perOrgNeedsYouTimeout)
					defer cancel()
					items, err := needsYou(ctx, db, profileID, root, name)
					if err != nil {
						rows[i].NeedsYouError = err.Error()
						return
					}
					n := len(items)
					rows[i].NeedsYou = &n
				}(i, name)
			}
			wg.Wait()
			totals := map[string]int{"orgs": len(rows), "running": 0, "queued": 0, "needs_you": 0}
			for _, r := range rows {
				if r.Running {
					totals["running"]++
				}
				totals["queued"] += r.Queued
				if r.NeedsYou != nil {
					totals["needs_you"] += *r.NeedsYou
				}
			}
			return printJSONValue(map[string]interface{}{
				"v": 1, "fast": fast, "serve_running": serveLive, "orgs": rows, "totals": totals,
			})
		},
	}
	c.Flags().BoolVar(&fast, "fast", false, "Local files and database only: skip needs_you (no monomind process)")
	return c
}
