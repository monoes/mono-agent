package orgbridge

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/workflow"
)

// maxEndpointBody bounds one delivery (plan §8 layer 4).
const maxEndpointBody = 1 << 20

// maxReplyBytes bounds an automation's reply, like grant tool outputs.
const maxReplyBytes = 16 * 1024

// EndpointDelivery is what monomind M2 POSTs to an automation role.
type EndpointDelivery struct {
	OrgName   string `json:"orgName"`
	Run       string `json:"run"`
	From      string `json:"from"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	MessageID string `json:"messageId"`
}

type pendingDelivery struct {
	row      orggrant.EndpointRow
	d        EndpointDelivery
	deadline time.Time
}

// Receiver is the automation-role endpoint (plan §7.2): it authenticates a
// delivery by its capability id, confirms the message really went through
// the org — a delivery counts only once a bus event with the same messageId
// addressed to that role appears, so a role that read the id from the org
// file cannot POST around org_send and its fence (C-4) — then starts the
// role's workflow and, when that run ends, replies to the sender.
type Receiver struct {
	DB     *sql.DB
	Store  workflow.WorkflowStore
	Mux    *Mux
	RootOf func(profileID string) string
	Resume func(executionID string) error
	Logf   func(format string, args ...interface{})

	// VerifyWindow is how long a delivery may wait for its bus event.
	VerifyWindow time.Duration

	mu         sync.Mutex
	pending    map[string]*pendingDelivery // messageId -> delivery
	seen       map[string]time.Time        // messageId|org:role -> bus event seen
	dispatched map[string]time.Time        // messageId -> dispatched
	subs       map[tailKey]func()
}

func (r *Receiver) init() {
	if r.pending == nil {
		r.pending = map[string]*pendingDelivery{}
		r.seen = map[string]time.Time{}
		r.dispatched = map[string]time.Time{}
		r.subs = map[tailKey]func(){}
	}
	if r.VerifyWindow <= 0 {
		r.VerifyWindow = time.Minute
	}
	if r.Logf == nil {
		r.Logf = func(string, ...interface{}) {}
	}
}

// Register mounts POST /org-endpoint/{id}. It has its own authentication
// and is served whether or not the API allows mutations.
func (r *Receiver) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /org-endpoint/{id}", r.ServeHTTP)
}

func writeJSONStatus(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ServeHTTP handles one delivery.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.init()
	r.mu.Unlock()
	ctx := req.Context()
	row, err := orggrant.NewStore(r.DB).LookupEndpoint(ctx, req.PathValue("id"))
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "unknown endpoint"})
		return
	}
	if msg, status := checkEndpointAuth(req, row); status != 0 {
		writeJSONStatus(w, status, map[string]string{"error": msg})
		return
	}
	var d EndpointDelivery
	if err := json.NewDecoder(io.LimitReader(req.Body, maxEndpointBody)).Decode(&d); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "body must be a JSON delivery"})
		return
	}
	if d.OrgName != row.OrgName || (d.To != row.RoleID && d.To != row.OrgName+":"+row.RoleID) {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "unknown endpoint"})
		return
	}
	if d.MessageID == "" || d.From == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "delivery needs from and messageId (monomind with capability org-endpoint-roles)"})
		return
	}

	r.mu.Lock()
	if _, done := r.dispatched[d.MessageID]; done {
		r.mu.Unlock()
		writeJSONStatus(w, http.StatusOK, map[string]interface{}{"accepted": true, "duplicate": true})
		return
	}
	r.ensureSubscribedLocked(*row)
	verified := r.seenLocked(d.MessageID, row.OrgName, row.RoleID)
	if !verified {
		r.pending[d.MessageID] = &pendingDelivery{row: *row, d: d, deadline: time.Now().Add(r.VerifyWindow)}
	} else {
		r.dispatched[d.MessageID] = time.Now()
	}
	r.mu.Unlock()

	if verified {
		r.dispatch(context.Background(), *row, d)
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]interface{}{"accepted": true, "verified": verified})
}

// checkEndpointAuth requires the bearer from the endpoint's credential file
// when one is set, and refuses non-loopback callers otherwise.
func checkEndpointAuth(req *http.Request, row *orggrant.EndpointRow) (string, int) {
	if row.CredentialFile != "" {
		info, err := os.Stat(row.CredentialFile)
		if err != nil {
			return "endpoint credential unavailable", http.StatusServiceUnavailable
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return "endpoint credential file must be mode 0600", http.StatusServiceUnavailable
		}
		want, err := os.ReadFile(row.CredentialFile)
		if err != nil {
			return "endpoint credential unavailable", http.StatusServiceUnavailable
		}
		got := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(want))), []byte(got)) != 1 {
			return "missing or invalid bearer credential", http.StatusUnauthorized
		}
		return "", 0
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "non-loopback deliveries need a credential file on the endpoint", http.StatusForbidden
	}
	return "", 0
}

func seenKey(messageID, org, role string) string { return messageID + "|" + org + ":" + role }

func (r *Receiver) seenLocked(messageID, org, role string) bool {
	_, ok := r.seen[seenKey(messageID, org, role)]
	return ok
}

func (r *Receiver) ensureSubscribedLocked(row orggrant.EndpointRow) {
	root := r.RootOf(row.ProfileID)
	k := tailKey{root, row.OrgName}
	if _, ok := r.subs[k]; ok {
		return
	}
	org := row.OrgName
	r.subs[k] = r.Mux.Subscribe(root, org, func(ev Event) { r.onEvent(org, ev) })
}

// onEvent marks bus-confirmed messages and dispatches deliveries waiting
// for them.
func (r *Receiver) onEvent(org string, ev Event) {
	if (ev.Type != "message" && ev.Type != "xorg") || ev.MessageID() == "" || ev.To == "" {
		return
	}
	role := ev.To
	if i := strings.IndexByte(role, ':'); i >= 0 {
		if role[:i] != org {
			return
		}
		role = role[i+1:]
	}
	r.mu.Lock()
	r.init()
	r.seen[seenKey(ev.MessageID(), org, role)] = time.Now()
	p := r.pending[ev.MessageID()]
	if p != nil && p.row.OrgName == org && p.row.RoleID == role {
		delete(r.pending, ev.MessageID())
		r.dispatched[ev.MessageID()] = time.Now()
	} else {
		p = nil
	}
	r.mu.Unlock()
	if p != nil {
		r.dispatch(context.Background(), p.row, p.d)
	}
}

// Sweep refuses deliveries whose bus event never appeared, prunes old
// bookkeeping, and sends replies for finished automation runs.
func (r *Receiver) Sweep(ctx context.Context) {
	r.mu.Lock()
	r.init()
	now := time.Now()
	var expired []*pendingDelivery
	for id, p := range r.pending {
		if now.After(p.deadline) {
			expired = append(expired, p)
			delete(r.pending, id)
		}
	}
	for k, at := range r.seen {
		if now.Sub(at) > 10*time.Minute {
			delete(r.seen, k)
		}
	}
	for k, at := range r.dispatched {
		if now.Sub(at) > time.Hour {
			delete(r.dispatched, k)
		}
	}
	r.mu.Unlock()
	for _, p := range expired {
		r.Logf("orgbridge: endpoint %s:%s: refused message %s — no matching bus event (direct POST?)", p.row.OrgName, p.row.RoleID, p.d.MessageID)
		_ = NewLedger(r.DB).Refuse(ctx, Call{ProfileID: p.row.ProfileID, Direction: DirEndpointIn, OrgName: p.row.OrgName,
			RoleID: p.row.RoleID, WorkflowID: p.row.WorkflowID, EndpointID: p.row.ID}, StatusRefusedGrant)
	}
	if err := r.sendReplies(ctx); err != nil {
		r.Logf("orgbridge: replies: %v", err)
	}
}

// Run sweeps every few seconds until ctx ends.
func (r *Receiver) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			for k, u := range r.subs {
				u()
				delete(r.subs, k)
			}
			r.mu.Unlock()
			return
		case <-t.C:
			r.Sweep(ctx)
		}
	}
}

// dispatch applies loop control and starts the automation role's workflow
// — or, when the message answers an org.ask, stores the reply instead.
func (r *Receiver) dispatch(ctx context.Context, row orggrant.EndpointRow, d EndpointDelivery) {
	if ref := AskRef(d.Subject, d.Body); ref != "" {
		asks := NewAskStore(r.DB)
		if a, err := asks.Get(ctx, ref); err == nil && a != nil && a.OrgName == row.OrgName && a.EndpointRoleID == row.RoleID {
			reply := map[string]interface{}{"from": d.From, "subject": d.Subject, "body": StripTrace(d.Body)}
			if ok, _ := asks.MarkReplied(ctx, a.ID, reply); ok && r.Resume != nil {
				_ = r.Resume(a.ExecutionID)
			}
			return
		}
	}
	tr, _ := ParseTrace(d.Body)
	origin := row.OrgName
	if i := strings.IndexByte(d.From, ':'); i > 0 {
		origin = d.From[:i]
	}
	ledger := NewLedger(r.DB)
	adm, err := ledger.Admit(ctx, Call{
		ProfileID: row.ProfileID, Trace: tr, OriginOrg: origin, Direction: DirEndpointIn, OrgName: row.OrgName,
		RoleID: row.RoleID, WorkflowID: row.WorkflowID, EndpointID: row.ID, RunID: d.Run,
	}, Limits{})
	if err != nil {
		r.Logf("orgbridge: endpoint %s:%s: %v", row.OrgName, row.RoleID, err)
		return
	}
	if !adm.OK() {
		r.Logf("orgbridge: endpoint %s:%s: %s", row.OrgName, row.RoleID, adm.Reason)
		return
	}
	exec, err := workflow.CreateUnownedExecution(ctx, r.Store, workflow.UnownedExecutionOptions{
		WorkflowID: row.WorkflowID, ProfileID: row.ProfileID, TriggerType: workflow.TriggerTypeOrgMessage, AllowInactive: true,
		TriggerData: map[string]interface{}{
			"trigger_type": workflow.TriggerTypeOrgMessage,
			"org_message": map[string]interface{}{
				"org": row.OrgName, "run": d.Run, "from": d.From, "to": d.To, "role": row.RoleID,
				"subject": d.Subject, "body": StripTrace(d.Body), "message_id": d.MessageID,
			},
			"text":  StripTrace(d.Body),
			"trace": map[string]interface{}{"chain_id": adm.Trace.ChainID, "hop": adm.Trace.Hop},
		},
	})
	if err != nil {
		_ = ledger.SetStatus(ctx, adm.ID, StatusError)
		r.Logf("orgbridge: endpoint %s:%s: start workflow: %v", row.OrgName, row.RoleID, err)
		return
	}
	_ = ledger.SetExecution(ctx, adm.ID, exec.ID)
}

type replyJob struct {
	profileID, org, role, executionID, chainID string
	hop                                        int
}

// sendReplies answers every finished automation-role run that has not
// replied yet (the reply's own endpoint_reply crossing is the marker; the
// run's own org.send crossings are workflow_out and do not count).
func (r *Receiver) sendReplies(ctx context.Context) error {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT c.profile_id, c.org_name, c.role_id, c.execution_id, c.chain_id, c.hop
		FROM org_bridge_calls c JOIN workflow_executions e ON e.id = c.execution_id
		WHERE c.direction = ? AND c.status = 'ok' AND e.status IN ('SUCCESS', 'FAILED', 'CANCELLED')
		  AND NOT EXISTS (SELECT 1 FROM org_bridge_calls x WHERE x.execution_id = c.execution_id AND x.direction = ?)`,
		DirEndpointIn, DirEndpointReply)
	if err != nil {
		return err
	}
	var jobs []replyJob
	for rows.Next() {
		var j replyJob
		if err := rows.Scan(&j.profileID, &j.org, &j.role, &j.executionID, &j.chainID, &j.hop); err == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	for _, j := range jobs {
		if err := r.reply(ctx, j); err != nil {
			r.Logf("orgbridge: reply for %s: %v", j.executionID, err)
		}
	}
	return nil
}

func (r *Receiver) reply(ctx context.Context, j replyJob) error {
	exec, err := r.Store.GetExecution(ctx, j.executionID)
	if err != nil || exec == nil {
		return fmt.Errorf("load execution: %v", err)
	}
	msg, _ := exec.TriggerData["org_message"].(map[string]interface{})
	from, _ := msg["from"].(string)
	subject, _ := msg["subject"].(string)
	if from == "" {
		return errors.New("execution has no org message to reply to")
	}
	replyMode := "last_node"
	root := r.RootOf(j.profileID)
	if doc, err := orgdesign.Load(root, j.org); err == nil {
		if role, _ := doc.FindRole(j.role); role != nil && role.Automation != nil && role.Automation.Reply != "" {
			replyMode = role.Automation.Reply
		}
	}
	body := ReplyBody(exec, replyMode)
	targetOrg, to := j.org, from
	if i := strings.IndexByte(from, ':'); i > 0 {
		targetOrg, to = from[:i], from[i+1:]
	}
	_, err = Send(ctx, NewLedger(r.DB), SendRequest{
		ProfileID: j.profileID, Root: root, Org: targetOrg, To: to, From: j.org + ":" + j.role,
		Subject: "re: " + subject, Body: body, Trace: Trace{ChainID: j.chainID, Hop: j.hop},
		OriginOrg: j.org, Direction: DirEndpointReply, WorkflowID: exec.WorkflowID, ExecutionID: exec.ID,
	})
	return err
}

// ReplyBody renders a finished run as the automation role's reply: the
// chosen node's output items, redacted and bounded, or the failure.
func ReplyBody(exec *workflow.WorkflowExecution, mode string) string {
	if exec.Status != "SUCCESS" {
		b, _ := json.Marshal(map[string]interface{}{"status": strings.ToLower(exec.Status), "error": exec.ErrorMessage, "execution_id": exec.ID})
		return string(b)
	}
	nodes := append([]workflow.WorkflowExecutionNode(nil), exec.Nodes...)
	sort.SliceStable(nodes, func(i, k int) bool {
		a, b := nodes[i].FinishedAt, nodes[k].FinishedAt
		if a == nil || b == nil {
			return a != nil
		}
		return a.Before(*b)
	})
	var pick *workflow.WorkflowExecutionNode
	want := strings.TrimPrefix(mode, "node:")
	for i := range nodes {
		n := &nodes[i]
		if n.Status != "SUCCESS" {
			continue
		}
		if strings.HasPrefix(mode, "node:") {
			if n.NodeName == want {
				pick = n
			}
			continue
		}
		pick = n // last finished successful node
	}
	out := map[string]interface{}{"status": "success", "execution_id": exec.ID}
	if pick != nil {
		items := make([]interface{}, 0, len(pick.OutputItems))
		for _, it := range pick.OutputItems {
			items = append(items, workflow.RedactItemJSON(it).JSON)
		}
		out["node"] = pick.NodeName
		out["items"] = items
	}
	b, _ := json.Marshal(out)
	if len(b) > maxReplyBytes {
		b, _ = json.Marshal(map[string]interface{}{
			"status": "success", "execution_id": exec.ID, "node": out["node"], "truncated": true,
			"preview": string(b[:maxReplyBytes-512]),
		})
	}
	return string(b)
}
