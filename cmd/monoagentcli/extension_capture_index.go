package main

import (
	"context"
	"database/sql"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/captureindex"
	"github.com/monoes/mono-agent/internal/extension"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// Capture indexing in the bridge.
//
// A page saved from the extension has to be searchable from "Ask your
// brain" and from chat within seconds, with no Index button. The bridge is
// the process that writes the capture, so it is the one that queues its
// indexing: every envelope that lands for a profile queues a pass over that
// profile's captures (internal/captureindex), which ingests them into the
// profile's own monomind store and records Indexed or the error on the
// document row. The logic is the CLI's — `profile documents index --all`
// runs the very same pass — the bridge only decides when.

// captureIndexStartupDelay is how long a bridge waits after starting
// before it retries every profile's unindexed captures: long enough that
// indexing never competes with the bridge and the daemon coming up.
const captureIndexStartupDelay = 30 * time.Second

// openCaptureIndexDB opens the database for one indexing pass.
func openCaptureIndexDB() (*sql.DB, func(), error) {
	db, err := openProfileDB(defaultDBPath)
	if err != nil {
		return nil, nil, err
	}
	return db.DB, func() { db.Close() }, nil
}

// newCaptureIndexQueue is the bridge's indexing queue. It starts on first
// use, so a bridge that never sees a capture never runs a worker.
func newCaptureIndexQueue(logf func(string, ...any)) *captureindex.Queue {
	return &captureindex.Queue{Indexer: &captureindex.Indexer{}, Open: openCaptureIndexDB, Logf: logf}
}

// handleIndexCapture is the after-write hook's indexing half: queue a pass
// for the capture's profile. A capture saved to the shared inbox names no
// profile and is left to monomind's own inbox sweep, as before.
func handleIndexCapture(q *captureindex.Queue, res *capture.Result) {
	if q == nil || res == nil || res.Meta.Profile == "" {
		return
	}
	q.Start()
	q.Enqueue(res.Meta.Profile)
}

// handleIndexSummary queues a pass for the profile of the capture in dir
// once its summary is written. The profile is read from the capture's own
// meta.json, as the summarizer knows only the directory.
func handleIndexSummary(q *captureindex.Queue, dir string) {
	meta, err := capture.ReadMeta(dir)
	if err != nil || meta.Profile == "" {
		return
	}
	q.Start()
	q.Enqueue(meta.Profile)
}

// sweepCaptureIndexAfter retries every profile's unindexed and failed
// captures once, after delay: the catch-up for captures that landed while
// no bridge was running, or failed on an older monomind. Cancelled with
// ctx.
func sweepCaptureIndexAfter(ctx context.Context, q *captureindex.Queue, delay time.Duration) {
	go func() {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		db, closeDB, err := openCaptureIndexDB()
		if err != nil {
			return // no database yet: nothing has been captured into a profile
		}
		profiles, err := profiledir.List(ctx, db)
		closeDB()
		if err != nil || len(profiles) == 0 {
			return
		}
		q.Start()
		for _, p := range profiles {
			q.EnqueueRetry(p.ID)
		}
	}()
}

// captureBrainStatus answers the Ask panel's "is anything searchable yet"
// for one profile.
func captureBrainStatus(ctx context.Context, profileID string) (*extension.BrainStatus, error) {
	db, closeDB, err := openCaptureIndexDB()
	if err != nil {
		return nil, err
	}
	defer closeDB()
	st, err := captureindex.StatusOf(ctx, db, profileID)
	if err != nil {
		return nil, err
	}
	return &extension.BrainStatus{Captures: st.Captures, Indexed: st.Indexed, Pending: st.Pending, Failed: st.Failed, LastError: st.LastError}, nil
}
