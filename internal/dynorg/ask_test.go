package dynorg

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

type fakeAnswers struct {
	mu      sync.Mutex
	answers map[string]string // agent/qid -> text
}

func (f *fakeAnswers) put(agent, qid, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.answers == nil {
		f.answers = map[string]string{}
	}
	f.answers[agent+"/"+qid] = text
}

func (f *fakeAnswers) Answer(_ context.Context, agent, qid string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.answers[agent+"/"+qid]
	if ok {
		delete(f.answers, agent+"/"+qid)
	}
	return t, ok, nil
}

// askingExec asks the user questions (when the prompt says so) and
// reports what it heard.
type askingExec struct {
	mu    sync.Mutex
	calls []monomind.ExecOptions
	asks  int
	hold  time.Duration
}

func (e *askingExec) exec(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, o)
	asks := e.asks
	e.mu.Unlock()
	on(monomind.Event{Type: monomind.EventStart})
	report := "did " + o.Prompt
	if strings.Contains(o.Prompt, "ask") && o.OnToolCall != nil {
		for i := 0; i < max(asks, 1); i++ {
			out, err := o.OnToolCall(ctx, ToolAskUser, json.RawMessage(`{"question":"Which database?"}`))
			if err != nil {
				report += " | error: " + err.Error()
				continue
			}
			report += " | " + out
		}
	}
	select {
	case <-time.After(e.hold):
	case <-ctx.Done():
		return &monomind.TurnResult{SawDone: true, StopReason: monomind.StopCancelled}, nil
	}
	if ctx.Err() != nil {
		return &monomind.TurnResult{SawDone: true, StopReason: monomind.StopCancelled}, nil
	}
	return okTurn(report), nil
}

var askModel = Model{Runtime: "claude", Model: "opus", FullAccess: true, CallerTools: true, CallerToolsFull: true}

func newAskConductor(t *testing.T, ex *askingExec, ans AnswerSource, roster ...Model) (*Conductor, *recEmitter) {
	t.Helper()
	if len(roster) == 0 {
		roster = []Model{askModel}
	}
	em := &recEmitter{}
	c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: roster, Lead: roster[0]}, Exec: ex.exec, Emit: em, Answers: ans,
		Limits: Limits{MaxAgents: 4, MaxConcurrent: 4}})
	t.Cleanup(c.Close)
	return c, em
}

func withFastAsk(t *testing.T, timeout time.Duration) {
	oldT, oldP := AskTimeout, askPoll
	AskTimeout, askPoll = timeout, 5*time.Millisecond
	t.Cleanup(func() { AskTimeout, askPoll = oldT, oldP })
}

func TestAskUserOnlyWhenTheExecCanTakeTools(t *testing.T) {
	ex := &askingExec{}
	noTools := Model{Runtime: "grok", Model: "g", FullAccess: true, CallerTools: true} // no tools under full access
	c, _ := newAskConductor(t, ex, &fakeAnswers{}, noTools)
	c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Wait: true})
	if len(ex.calls[0].Tools) != 0 || strings.Contains(ex.calls[0].SystemPrompt, "ask_user") {
		t.Errorf("a runtime without caller tools under full access must not get ask_user")
	}

	ex2 := &askingExec{}
	c2, _ := newAskConductor(t, ex2, nil)
	c2.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Wait: true})
	if len(ex2.calls[0].Tools) != 0 {
		t.Error("without an answer source there is no ask_user")
	}

	ex3 := &askingExec{}
	c3, _ := newAskConductor(t, ex3, &fakeAnswers{})
	c3.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Wait: true})
	o := ex3.calls[0]
	if len(o.Tools) != 1 || o.Tools[0].Name != ToolAskUser || !strings.Contains(o.SystemPrompt, "ask_user") {
		t.Errorf("ask_user missing: %+v", o.Tools)
	}
	if o.ToolTimeout <= AskTimeout || o.Env["MCP_TOOL_TIMEOUT"] == "" {
		t.Errorf("claude worker needs a tool timeout past AskTimeout: %v %v", o.ToolTimeout, o.Env)
	}
}

func TestAskUserAnswerPath(t *testing.T) {
	withFastAsk(t, 5*time.Second)
	ans := &fakeAnswers{}
	ex := &askingExec{}
	c, em := newAskConductor(t, ex, ans)
	c.Spawn(context.Background(), SpawnRequest{Brief: "ask then implement", Access: ProfileCoding})
	waitFor(t, func() bool { return len(em.find(chatevents.EventAgentMessage)) >= 2 })
	infos := c.Wait(context.Background(), []string{"w1"}, 10*time.Millisecond)
	if infos[0].Status != chatevents.AgentWaitingUser {
		t.Fatalf("status while asking = %s", infos[0].Status)
	}
	ans.put("w1", "q1", "Postgres")
	infos = c.Wait(context.Background(), []string{"w1"}, 5*time.Second)
	if infos[0].Status != chatevents.AgentDone || !strings.Contains(infos[0].Report, "The user answered: Postgres") {
		t.Fatalf("after answer = %+v", infos[0])
	}
	// waiting_user, the question, the answer, then working again; lease
	// reports (agent.status with the same status) may fall in between.
	order := []string{"agent.status:waiting_user", "agent.message", "agent.message", "agent.status:working"}
	events := em.types("w1")
	from := slices.Index(events, "agent.status:waiting_user")
	for _, want := range order {
		if from < 0 {
			break
		}
		i := slices.Index(events[from:], want)
		if i < 0 {
			from = -1
			break
		}
		from += i + 1
	}
	if from < 0 {
		t.Errorf("events\n got %s\nwant %v in that order", strings.Join(events, " "), order)
	}
	var q, a chatevents.AgentMessagePayload
	for _, p := range em.find(chatevents.EventAgentMessage) {
		m := p.(chatevents.AgentMessagePayload)
		switch m.Direction {
		case "question":
			q = m
		case "followup":
			a = m
		}
	}
	if q.QuestionID != "q1" || q.To != "user" || q.Text != "Which database?" {
		t.Errorf("question = %+v", q)
	}
	if a.QuestionID != "q1" || a.From != "user" || a.Text != "Postgres" {
		t.Errorf("answer = %+v", a)
	}
}

func TestAskUserLetsGoOfTheWriteLease(t *testing.T) {
	withFastAsk(t, 5*time.Second)
	ans := &fakeAnswers{}
	ex := &askingExec{}
	c, em := newAskConductor(t, ex, ans)
	c.Spawn(context.Background(), SpawnRequest{Brief: "ask then implement", Access: ProfileCoding})
	waitFor(t, func() bool { return len(em.find(chatevents.EventAgentMessage)) >= 2 })
	// w1 waits on the user; the second writer gets the write lease.
	info, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "implement b", Access: ProfileCoding, Wait: true})
	if info.Status != chatevents.AgentDone {
		t.Fatalf("second writer blocked while the first waited on the user: %+v", info)
	}
	ans.put("w1", "q1", "yes")
	if infos := c.Wait(context.Background(), []string{"w1"}, 5*time.Second); infos[0].Status != chatevents.AgentDone {
		t.Errorf("w1 = %+v", infos[0])
	}
}

func TestAskUserTimesOutAndStops(t *testing.T) {
	withFastAsk(t, 50*time.Millisecond)
	ex := &askingExec{}
	c, em := newAskConductor(t, ex, &fakeAnswers{})
	info, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "ask then implement", Wait: true})
	if info.Status != chatevents.AgentDone || !strings.Contains(info.Report, "didn't answer") {
		t.Errorf("timed-out question = %+v", info)
	}
	closed := false
	for _, p := range em.find(chatevents.EventAgentMessage) {
		if m := p.(chatevents.AgentMessagePayload); m.Direction == "followup" {
			if m.From == "user" {
				t.Error("a timeout must not be journaled as a user answer")
			}
			closed = m.From == "system" && m.QuestionID == "q1"
		}
	}
	if !closed {
		t.Error("a timed-out question must be closed in the journal (followup from system)")
	}

	withFastAsk(t, time.Hour)
	ex2 := &askingExec{}
	c2, em2 := newAskConductor(t, ex2, &fakeAnswers{})
	c2.Spawn(context.Background(), SpawnRequest{Brief: "ask then implement"})
	waitFor(t, func() bool { return len(em2.find(chatevents.EventAgentMessage)) >= 2 })
	c2.Stop("w1")
	if infos := c2.Wait(context.Background(), []string{"w1"}, 5*time.Second); infos[0].Status != chatevents.AgentCancelled {
		t.Errorf("stopped while asking = %+v", infos[0])
	}
}

func TestAskUserIsCapped(t *testing.T) {
	withFastAsk(t, 20*time.Millisecond)
	ex := &askingExec{asks: MaxQuestions + 1}
	c, _ := newAskConductor(t, ex, &fakeAnswers{})
	info, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "ask a lot", Wait: true})
	if !strings.Contains(info.Report, "already asked 3 questions") {
		t.Errorf("report = %s", info.Report)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

func TestWaitingWorkerHoldsNoSlotSoNoDeadlock(t *testing.T) {
	withFastAsk(t, 5*time.Second)
	ans := &fakeAnswers{}
	ex := &askingExec{hold: 80 * time.Millisecond}
	em := &recEmitter{}
	c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: []Model{askModel}, Lead: askModel}, Exec: ex.exec, Emit: em, Answers: ans,
		Limits: Limits{MaxAgents: 3, MaxConcurrent: 1}})
	defer c.Close()
	c.Spawn(context.Background(), SpawnRequest{Brief: "ask then implement a", Access: ProfileCoding})
	waitFor(t, func() bool { return len(em.find(chatevents.EventAgentMessage)) >= 2 })
	// B gets the write lease and the only slot while A waits on the user.
	c.Spawn(context.Background(), SpawnRequest{Brief: "implement b", Access: ProfileCoding})
	waitFor(t, func() bool {
		infos := c.Wait(context.Background(), []string{"w2"}, time.Millisecond)
		return infos[0].Status == chatevents.AgentWorking
	})
	ans.put("w1", "q1", "go") // A resumes while B still runs: it must queue, not deadlock
	infos := c.Wait(context.Background(), nil, 5*time.Second)
	for _, in := range infos {
		if in.Status != chatevents.AgentDone {
			t.Fatalf("deadlocked: %+v", infos)
		}
	}
	assertClean(t, c)
}

func TestTwoAskingWritersDontDeadlock(t *testing.T) {
	withFastAsk(t, 5*time.Second)
	ans := &fakeAnswers{}
	ex := &askingExec{hold: 20 * time.Millisecond}
	em := &recEmitter{}
	c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: []Model{askModel}, Lead: askModel}, Exec: ex.exec, Emit: em, Answers: ans,
		Limits: Limits{MaxAgents: 3, MaxConcurrent: 2}})
	defer c.Close()
	c.Spawn(context.Background(), SpawnRequest{Brief: "ask then implement a", Access: ProfileCoding})
	c.Spawn(context.Background(), SpawnRequest{Brief: "ask then implement b", Access: ProfileCoding})
	waitFor(t, func() bool { return len(em.find(chatevents.EventAgentMessage)) >= 4 })
	ans.put("w1", "q1", "a")
	ans.put("w2", "q1", "b")
	for _, in := range c.Wait(context.Background(), nil, 5*time.Second) {
		if in.Status != chatevents.AgentDone {
			t.Fatalf("deadlocked: %+v", in)
		}
	}
	assertClean(t, c)
}

func TestQuestionIDsNeverRepeatAcrossFollowUps(t *testing.T) {
	withFastAsk(t, 40*time.Millisecond)
	ans := &fakeAnswers{}
	ex := &askingExec{}
	c, em := newAskConductor(t, ex, ans)
	info, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "ask then implement", Wait: true})
	if !strings.Contains(info.Report, "didn't answer") {
		t.Fatalf("run 1 = %+v", info)
	}
	// A late answer to the closed q1 must not reach the next run's question.
	ans.put("w1", "q1", "LATE")
	withFastAsk(t, 5*time.Second)
	if _, err := c.Message(context.Background(), "w1", "ask again"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, p := range em.find(chatevents.EventAgentMessage) {
			if m := p.(chatevents.AgentMessagePayload); m.Direction == "question" && m.QuestionID == "q2" {
				return true
			}
		}
		return false
	})
	ans.put("w1", "q2", "right")
	infos := c.Wait(context.Background(), []string{"w1"}, 5*time.Second)
	if !strings.Contains(infos[0].Report, "The user answered: right") || strings.Contains(infos[0].Report, "LATE") {
		t.Errorf("run 2 report = %s", infos[0].Report)
	}
}

func TestStopWhileAskingClosesTheQuestion(t *testing.T) {
	withFastAsk(t, time.Hour)
	ex := &askingExec{}
	c, em := newAskConductor(t, ex, &fakeAnswers{})
	c.Spawn(context.Background(), SpawnRequest{Brief: "ask then implement", Access: ProfileCoding})
	waitFor(t, func() bool { return len(em.find(chatevents.EventAgentMessage)) >= 2 })
	if in := c.Wait(context.Background(), []string{"w1"}, time.Millisecond)[0]; in.Question != "Which database?" {
		t.Errorf("org_wait must show the open question: %+v", in)
	}
	c.Stop("w1")
	c.Wait(context.Background(), nil, 5*time.Second)
	closed := false
	for _, p := range em.find(chatevents.EventAgentMessage) {
		if m := p.(chatevents.AgentMessagePayload); m.Direction == "followup" && m.From == "system" && m.QuestionID == "q1" {
			closed = true
		}
	}
	if !closed {
		t.Error("a question cut short by a stop must be closed in the journal")
	}
	assertClean(t, c)
}

// assertClean checks no worker left a slot or a lease behind.
func assertClean(t *testing.T, c *Conductor) {
	t.Helper()
	if n := len(c.slots); n != 0 {
		t.Errorf("%d concurrency slots still taken", n)
	}
	for name, l := range map[string]*lease{"write": c.write, "browser": c.browser} {
		if !l.tryAcquire() {
			t.Errorf("the %s lease is still held", name)
			continue
		}
		l.release()
	}
}

// parallelAskExec asks two questions in one message: both tool calls run
// at once, as monomind.Exec runs a message's tool calls concurrently.
type parallelAskExec struct {
	mu      sync.Mutex
	results []string
}

func (e *parallelAskExec) exec(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
	on(monomind.Event{Type: monomind.EventStart})
	var wg sync.WaitGroup
	for _, q := range []string{"Which database?", "Which port?"} {
		wg.Add(1)
		go func(q string) {
			defer wg.Done()
			out, err := o.OnToolCall(ctx, ToolAskUser, json.RawMessage(`{"question":"`+q+`"}`))
			e.mu.Lock()
			if err != nil {
				out = "error: " + err.Error()
			}
			e.results = append(e.results, out)
			e.mu.Unlock()
		}(q)
	}
	wg.Wait()
	return okTurn("done"), nil
}

func TestParallelAsksKeepOneQuestionAndCleanAccounting(t *testing.T) {
	for _, mc := range []int{1, 2} {
		withFastAsk(t, 5*time.Second)
		ans := &fakeAnswers{}
		ex := &parallelAskExec{}
		em := &recEmitter{}
		c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: []Model{askModel}, Lead: askModel}, Exec: ex.exec, Emit: em, Answers: ans,
			Limits: Limits{MaxAgents: 2, MaxConcurrent: mc}})
		c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Access: ProfileCoding})
		waitFor(t, func() bool {
			for _, p := range em.find(chatevents.EventAgentMessage) {
				if p.(chatevents.AgentMessagePayload).Direction == "question" {
					return true
				}
			}
			return false
		})
		ans.put("w1", "q1", "yes")
		info := c.Wait(context.Background(), []string{"w1"}, 5*time.Second)[0]
		if info.Status != chatevents.AgentDone {
			t.Fatalf("mc=%d: worker stuck: %+v", mc, info)
		}
		ex.mu.Lock()
		got := strings.Join(ex.results, " | ")
		ex.mu.Unlock()
		if !strings.Contains(got, "The user answered: yes") || !strings.Contains(got, "already have an open question") {
			t.Errorf("mc=%d: tool results = %s", mc, got)
		}
		questions := 0
		for _, p := range em.find(chatevents.EventAgentMessage) {
			if p.(chatevents.AgentMessagePayload).Direction == "question" {
				questions++
			}
		}
		if questions != 1 {
			t.Errorf("mc=%d: %d questions journaled, want 1", mc, questions)
		}
		assertClean(t, c)
		c.Close()
	}
}
