package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// watcherResyncInterval is how often the daemon re-reads the profile list
// to keep its org-file watchers on the right folders. A profile created, a
// profile folder moved (App.MoveProfileFolder), or a profile removed while
// the daemon runs is picked up within this long, without a restart.
const watcherResyncInterval = 10 * time.Second

// orgWatch is one profile's org-file watcher and the folder it watches.
type orgWatch struct {
	root string
	w    *orgdesign.Watcher
}

// watchOrgFiles keeps one org-file watcher per profile until ctx ends,
// following the profile list as it changes.
func (s *orgServices) watchOrgFiles(ctx context.Context) {
	s.syncWatchers(ctx, false)
	interval := s.resyncInterval
	if interval <= 0 {
		interval = watcherResyncInterval
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.stopWatchers()
				return
			case <-ticker.C:
				s.syncWatchers(ctx, true)
			}
		}
	}()
}

// syncWatchers makes the running watchers match the profiles as they are
// now. A profile whose folder changed gets a watcher at the new folder; a
// profile that is new (or newly moved) is reconciled once first, since
// edits made there while nothing watched it were never seen. announce logs
// those changes, which the startup pass does not.
func (s *orgServices) syncWatchers(ctx context.Context, announce bool) {
	roots, err := profileRoots(s.db.DB)
	if err != nil {
		// A transient DB error must not stop every watcher.
		s.logf("org services: listing profiles: %v", err)
		return
	}
	want := make(map[string]string, len(roots))
	for _, pr := range roots {
		want[pr.ProfileID] = filepath.Clean(pr.Root)
	}

	s.mu.Lock()
	if s.watchers == nil {
		s.watchers = map[string]*orgWatch{}
	}
	var stale []*orgWatch
	for id, ow := range s.watchers {
		if root, ok := want[id]; !ok || root != ow.root {
			stale = append(stale, ow)
			delete(s.watchers, id)
			if announce {
				s.logf("org services: stopped watching %s at %s", id, ow.root)
			}
		}
	}
	var fresh []orgdecide.ProfileRoot
	for _, pr := range roots {
		if _, ok := s.watchers[pr.ProfileID]; !ok {
			fresh = append(fresh, pr)
		}
	}
	s.mu.Unlock()

	// Outside the lock: a watcher's callback reconciles, which takes s.mu.
	for _, ow := range stale {
		ow.w.Stop()
	}
	for _, pr := range fresh {
		if ctx.Err() != nil {
			return
		}
		pr := pr
		w := orgdesign.NewWatcher(orgdesign.OrgsDir(pr.Root), s.watchInterval, func(c orgdesign.Change) {
			if c.Deleted || c.Doc == nil {
				return
			}
			s.reconcileDoc(ctx, pr, c.Doc, false)
		})
		// Started before the reconcile, so the reconcile's own saves are
		// marked as self-writes on it rather than seen as edits.
		w.Start()
		s.mu.Lock()
		s.watchers[pr.ProfileID] = &orgWatch{root: filepath.Clean(pr.Root), w: w}
		s.mu.Unlock()
		if announce {
			s.logf("org services: watching %s at %s", pr.ProfileID, pr.Root)
		}
		s.reconcileProfile(ctx, pr)
	}
}

// stopWatchers stops every watcher, for shutdown.
func (s *orgServices) stopWatchers() {
	s.mu.Lock()
	all := s.watchers
	s.watchers = nil
	s.mu.Unlock()
	for _, ow := range all {
		ow.w.Stop()
	}
}

// markSelfWrite tells the profile's watcher that sha is the daemon's own
// save of org name, so it is not reconciled a second time.
func (s *orgServices) markSelfWrite(profileID, name, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ow := s.watchers[profileID]; ow != nil {
		ow.w.MarkSelfWrite(name, sha)
	}
}
