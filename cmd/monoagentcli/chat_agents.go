package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// leadAgentID names the lead in `--agent` filters and by-agent
// transcripts: its events carry no agentId.
const leadAgentID = "lead"

// eventAgentID is the dynamic-org agent an event belongs to (#226): the
// payload's agentId, or "lead" when it has none.
func eventAgentID(r chatevents.Record) string {
	var p struct {
		AgentID string `json:"agentId"`
	}
	if json.Unmarshal(r.Payload, &p) == nil && p.AgentID != "" {
		return p.AgentID
	}
	return leadAgentID
}

// agentMessage is one brief, follow-up, question or report in a by-agent
// transcript.
type agentMessage struct {
	Direction string `json:"direction"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Text      string `json:"text"`
}

// agentTranscript is one agent's side of a dynamic-org turn: who it was,
// what it was asked and reported, and the text it wrote (#258).
type agentTranscript struct {
	AgentID  string         `json:"agent_id"`
	ParentID string         `json:"parent_id,omitempty"`
	Role     string         `json:"role,omitempty"`
	Runtime  string         `json:"runtime,omitempty"`
	Model    string         `json:"model,omitempty"`
	Status   string         `json:"status,omitempty"`
	Messages []agentMessage `json:"messages"`
	Text     string         `json:"text"`
	Tools    int            `json:"tools"`
}

// agentTranscripts splits a turn's events by agent, the lead first and
// then each worker in the order it was spawned. A worker's text parts are
// joined with blank lines between them.
func agentTranscripts(recs []chatevents.Record) []agentTranscript {
	byID := map[string]*agentTranscript{}
	var order []string
	parts := map[string]string{} // agent -> its last part id
	get := func(id string) *agentTranscript {
		if t := byID[id]; t != nil {
			return t
		}
		t := &agentTranscript{AgentID: id, Messages: []agentMessage{}}
		byID[id] = t
		order = append(order, id)
		return t
	}
	get(leadAgentID)
	text := map[string]*strings.Builder{}
	for _, r := range recs {
		id := eventAgentID(r)
		switch r.Type {
		case chatevents.EventAgentSpawned:
			var p chatevents.AgentSpawnedPayload
			if json.Unmarshal(r.Payload, &p) == nil {
				t := get(id)
				t.ParentID, t.Role, t.Runtime, t.Model = p.ParentID, p.Role, p.Runtime, p.Model
			}
		case chatevents.EventAgentReassigned:
			var p chatevents.AgentReassignedPayload
			if json.Unmarshal(r.Payload, &p) == nil {
				t := get(id)
				t.Runtime, t.Model = p.ToRuntime, p.ToModel
			}
		case chatevents.EventAgentStatus:
			var p chatevents.AgentStatusPayload
			if json.Unmarshal(r.Payload, &p) == nil {
				get(id).Status = p.To
			}
		case chatevents.EventAgentMessage:
			var p chatevents.AgentMessagePayload
			if json.Unmarshal(r.Payload, &p) == nil {
				t := get(id)
				t.Messages = append(t.Messages, agentMessage{Direction: p.Direction, From: p.From, To: p.To, Text: p.Text})
			}
		case chatevents.EventAssistantDelta:
			var p chatevents.AssistantDeltaPayload
			if json.Unmarshal(r.Payload, &p) != nil {
				continue
			}
			get(id)
			b := text[id]
			if b == nil {
				b = &strings.Builder{}
				text[id] = b
			}
			if b.Len() > 0 && parts[id] != p.PartID {
				b.WriteString("\n\n")
			}
			parts[id] = p.PartID
			b.WriteString(p.Text)
		case chatevents.EventToolStarted:
			get(id).Tools++
		}
	}
	out := make([]agentTranscript, 0, len(order))
	for _, id := range order {
		t := byID[id]
		if b := text[id]; b != nil {
			t.Text = b.String()
		}
		out = append(out, *t)
	}
	return out
}

// allTurnEvents reads every event of a turn, page by page.
func allTurnEvents(store *ai.AIStore, profileID, conversationID, turnID string) ([]chatevents.Record, error) {
	var out []chatevents.Record
	var after int64
	for {
		evs, err := store.GetEvents(conversationID, turnID, profileID, after, 1000)
		if err != nil {
			return nil, err
		}
		for _, ev := range evs {
			out = append(out, ev.Record())
			after = ev.Seq
		}
		if len(evs) < 1000 {
			return out, nil
		}
	}
}

// printAgentTranscripts is `chat history transcript --by-agent`'s text
// output.
func printAgentTranscripts(items []agentTranscript) {
	for i, t := range items {
		if i > 0 {
			fmt.Println()
		}
		head := t.AgentID
		if extra := strings.Join(nonEmpty(t.Role, strings.Trim(t.Runtime+"/"+t.Model, "/"), t.Status), ", "); extra != "" {
			head += " (" + extra + ")"
		}
		fmt.Printf("== %s ==\n", head)
		for _, m := range t.Messages {
			fmt.Printf("[%s] %s\n", m.Direction, m.Text)
		}
		if t.Text != "" {
			fmt.Println(t.Text)
		}
		if t.Tools > 0 {
			fmt.Printf("(%d tool calls)\n", t.Tools)
		}
	}
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
