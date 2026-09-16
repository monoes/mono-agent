package orggroup

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// maxReportBytes bounds a synthetic report-up.
const maxReportBytes = 4096

// ReportUpWatcher is the hard fallback for report-up (plan §7.5): when a
// child org's run stops without its boss having messaged the holding org's
// Initiator, it sends the child's run report to the Initiator itself, so
// the parent is never left waiting on a child that finished silently.
type ReportUpWatcher struct {
	DB       *sql.DB
	Mux      *orgbridge.Mux
	Roots    func(ctx context.Context) ([]ProfileRoot, error)
	Report   func(ctx context.Context, root, org string) (json.RawMessage, error)
	Send     func(ctx context.Context, req orgbridge.SendRequest) error
	Interval time.Duration
	Logf     func(format string, args ...interface{})

	mu     sync.Mutex
	subs   map[string]func()          // root|child -> unsubscribe
	links  map[string]childLink       // root|child -> parent
	sentUp map[string]map[string]bool // root|child -> run -> reported
}

// ProfileRoot is one profile's org folder.
type ProfileRoot struct {
	ProfileID string
	Root      string
}

type childLink struct {
	profileID, root, child, childBoss, parent, initiator string
}

func (w *ReportUpWatcher) defaults() {
	if w.subs == nil {
		w.subs, w.links, w.sentUp = map[string]func(){}, map[string]childLink{}, map[string]map[string]bool{}
	}
	if w.Interval <= 0 {
		w.Interval = 30 * time.Second
	}
	if w.Logf == nil {
		w.Logf = func(string, ...interface{}) {}
	}
	if w.Report == nil {
		w.Report = func(ctx context.Context, root, org string) (json.RawMessage, error) {
			return monomind.OrgReport(ctx, root, org, false, "")
		}
	}
	if w.Send == nil {
		w.Send = func(ctx context.Context, req orgbridge.SendRequest) error {
			_, err := orgbridge.Send(ctx, orgbridge.NewLedger(w.DB), req)
			return err
		}
	}
}

// Run keeps subscriptions aligned with the holding orgs on disk.
func (w *ReportUpWatcher) Run(ctx context.Context) {
	w.mu.Lock()
	w.defaults()
	w.mu.Unlock()
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		w.Sync(ctx)
		select {
		case <-ctx.Done():
			w.mu.Lock()
			for k, u := range w.subs {
				u()
				delete(w.subs, k)
			}
			w.mu.Unlock()
			return
		case <-t.C:
		}
	}
}

// Sync subscribes to every child org of every holding org.
func (w *ReportUpWatcher) Sync(ctx context.Context) {
	w.mu.Lock()
	w.defaults()
	w.mu.Unlock()
	roots, err := w.Roots(ctx)
	if err != nil {
		return
	}
	want := map[string]childLink{}
	for _, pr := range roots {
		docs, _, err := orgdesign.LoadAll(pr.Root)
		if err != nil {
			continue
		}
		byName := map[string]*orgdesign.Doc{}
		for _, d := range docs {
			byName[d.Name] = d
		}
		for _, h := range docs {
			initiator, ok := h.RootRole()
			if !h.IsHolding() || !ok {
				continue
			}
			for _, c := range h.ChildOrgs {
				child := byName[c.Org]
				if child == nil {
					continue
				}
				boss, ok := child.RootRole()
				if !ok {
					continue
				}
				want[pr.Root+"|"+c.Org] = childLink{pr.ProfileID, pr.Root, c.Org, boss.ID, h.Name, initiator.ID}
			}
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, u := range w.subs {
		if _, ok := want[k]; !ok {
			u()
			delete(w.subs, k)
			delete(w.links, k)
		}
	}
	for k, l := range want {
		w.links[k] = l
		if _, ok := w.subs[k]; ok {
			continue
		}
		key := k
		w.subs[k] = w.Mux.Subscribe(l.root, l.child, func(ev orgbridge.Event) { w.Handle(ctx, key, ev) })
	}
}

// Handle processes one child-org event (exported for tests).
func (w *ReportUpWatcher) Handle(ctx context.Context, key string, ev orgbridge.Event) {
	w.mu.Lock()
	w.defaults()
	l, ok := w.links[key]
	if !ok {
		w.mu.Unlock()
		return
	}
	runs := w.sentUp[key]
	if runs == nil {
		runs = map[string]bool{}
		w.sentUp[key] = runs
	}
	switch {
	case (ev.Type == "xorg" || ev.Type == "message") && strings.HasPrefix(ev.To, l.parent+":"):
		runs[ev.Run] = true
		w.mu.Unlock()
		return
	case ev.Type == "status" && ev.From == "" && ev.Msg == "org stopped":
		if runs[ev.Run] {
			w.mu.Unlock()
			return
		}
		runs[ev.Run] = true
	default:
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()

	body := "The child org's run ended without a summary from its boss; this is its run report.\n"
	if raw, err := w.Report(ctx, l.root, l.child); err == nil {
		text := string(raw)
		if len(text) > maxReportBytes {
			text = text[:maxReportBytes] + "…(truncated)"
		}
		body += text
	} else {
		body += "(report unavailable: " + err.Error() + ")"
	}
	if err := w.Send(ctx, orgbridge.SendRequest{
		ProfileID: l.profileID, Root: l.root, Org: l.parent, To: l.initiator, From: l.child + ":" + l.childBoss,
		Subject: "report-up: " + l.child + " run " + ev.Run + " ended", Body: body, OriginOrg: l.child,
	}); err != nil {
		w.Logf("orggroup: report-up for %s: %v", l.child, err)
	}
}
