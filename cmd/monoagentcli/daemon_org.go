package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// profileRoots lists every profile's org folder (the daemon spans profiles).
func profileRoots(db *sql.DB) ([]orgdecide.ProfileRoot, error) {
	rows, err := db.Query(`SELECT id FROM profiles`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if !containsString(ids, "default") {
		ids = append(ids, "default")
	}
	sort.Strings(ids)
	var out []orgdecide.ProfileRoot
	for _, id := range ids {
		if root := profiledir.Root(db, id); root != "" {
			out = append(out, orgdecide.ProfileRoot{ProfileID: id, Root: root})
		}
	}
	return out, nil
}

// orgServices are the org-side services `monoagentcli daemon` hosts next
// to the workflow engine.
type orgServices struct {
	db       *storage.Database
	mux      *orgbridge.Mux
	trigger  *orgbridge.TriggerSource
	receiver *orgbridge.Receiver
	logf     func(format string, args ...interface{})

	mu       sync.Mutex
	watchers []*orgdesign.Watcher
}

// newOrgServices registers trigger.org with the engine; call before
// engine.Start so restored workflows activate it.
func newOrgServices(db *storage.Database, engine *workflow.WorkflowEngine) *orgServices {
	mux := orgbridge.NewMux(nil)
	s := &orgServices{
		db: db, mux: mux, trigger: orgbridge.NewTriggerSource(mux, db.DB),
		logf: func(format string, args ...interface{}) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
	}
	engine.RegisterTriggerSource(orgbridge.TriggerNodeType, s.trigger)
	s.receiver = &orgbridge.Receiver{
		DB: db.DB, Store: newHybridStore(db), Mux: mux, Resume: engine.ResumeExecution, Logf: s.logf,
		RootOf: func(profileID string) string { return profiledir.Root(db.DB, profileID) },
	}
	return s
}

// registerRoutes mounts the automation-role endpoint receiver on the
// daemon's HTTP API.
func (s *orgServices) registerRoutes(mux *http.ServeMux) { s.receiver.Register(mux) }

// start runs reconcile, the org file watchers, the waker, and the decision
// service until ctx ends.
func (s *orgServices) start(ctx context.Context, engine *workflow.WorkflowEngine) {
	roots, err := profileRoots(s.db.DB)
	if err != nil {
		s.logf("org services: listing profiles: %v", err)
	}
	for _, pr := range roots {
		s.reconcileProfile(ctx, pr)
		pr := pr
		w := orgdesign.NewWatcher(orgdesign.OrgsDir(pr.Root), 0, func(c orgdesign.Change) {
			if c.Deleted || c.Doc == nil {
				return
			}
			s.reconcileDoc(ctx, pr, c.Doc)
		})
		w.Start()
		s.mu.Lock()
		s.watchers = append(s.watchers, w)
		s.mu.Unlock()
	}

	waker := &orgbridge.Waker{
		DB: s.db.DB, Mux: s.mux, Resume: engine.ResumeExecution,
		RootOf: func(profileID string) string { return profiledir.Root(s.db.DB, profileID) },
		Logf:   s.logf,
	}
	go waker.Run(ctx)
	go s.receiver.Run(ctx)

	svc := orgdecide.NewService(s.db.DB, func(context.Context) ([]orgdecide.ProfileRoot, error) {
		return profileRoots(s.db.DB)
	})
	svc.Logf = s.logf
	svc.WorkflowFacts = func(ctx context.Context, profileID, workflowID string) string {
		return workflowFacts(ctx, s.db, workflowID)
	}
	go svc.Run(ctx)

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, w := range s.watchers {
			w.Stop()
		}
	}()
}

// reconcileProfile reconciles every org of a profile at startup, so grants,
// providers, approvalTools, and autonomy agree with the enforcement rows
// even after the org files were edited while no daemon ran (C-3, C-54).
func (s *orgServices) reconcileProfile(ctx context.Context, pr orgdecide.ProfileRoot) {
	docs, bad, err := orgdesign.LoadAll(pr.Root)
	if err != nil {
		return
	}
	for name, e := range bad {
		s.logf("org services: %s/%s: unreadable org file: %v", pr.ProfileID, name, e)
	}
	for _, d := range docs {
		s.reconcileDoc(ctx, pr, d)
	}
}

func (s *orgServices) reconcileDoc(ctx context.Context, pr orgdecide.ProfileRoot, d *orgdesign.Doc) {
	rep, err := orggrant.Reconcile(ctx, orggrant.NewStore(s.db.DB), d, orggrant.GenOptions{
		ProfileID: pr.ProfileID, CLIPath: selfExecutable(), APIAddr: orgAPIAddr(s.db),
	})
	if err != nil {
		s.logf("org services: reconcile %s: %v", d.Name, err)
		return
	}
	auto, err := orgdecide.ReconcileAutonomy(ctx, orgdecide.NewStore(s.db.DB), pr.ProfileID, d)
	if err != nil {
		s.logf("org services: autonomy reconcile %s: %v", d.Name, err)
		return
	}
	if auto.Ignored != "" {
		s.logf("org services: %s: %s", d.Name, auto.Ignored)
	}
	if !rep.Changed && !auto.DocChanged {
		return
	}
	for _, f := range rep.Findings {
		s.logf("org services: %s: %s %s", d.Name, f.Kind, f.Detail)
	}
	sha, err := orgdesign.Save(pr.Root, d)
	if err != nil {
		s.logf("org services: saving reconciled %s: %v", d.Name, err)
		return
	}
	s.mu.Lock()
	for _, w := range s.watchers {
		w.MarkSelfWrite(d.Name, sha)
	}
	s.mu.Unlock()
}

// workflowFacts describes a workflow for the decider prompt from the DB.
func workflowFacts(ctx context.Context, db *storage.Database, workflowID string) string {
	wf, err := newHybridStore(db).GetWorkflow(ctx, workflowID)
	if err != nil || wf == nil {
		return "workflow " + workflowID + " (not found)"
	}
	types := make([]string, 0, len(wf.Nodes))
	for _, n := range wf.Nodes {
		types = append(types, n.Type)
	}
	out := orggrant.OutboundNodes(wf)
	outbound := "none"
	if len(out) > 0 {
		outbound = strings.Join(out, ", ")
	}
	return fmt.Sprintf("workflow %q (%s); nodes: %s; outbound nodes: %s", wf.Name, wf.ID, strings.Join(types, ", "), outbound)
}
