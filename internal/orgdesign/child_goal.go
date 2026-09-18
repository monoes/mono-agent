package orgdesign

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Child goal check (plan caveat C-36).
//
// monomind wakes a stopped org that receives a message with a full
// goal-driven run (autoWake = startOrg with the org's own goal), not a run
// scoped to the message. A child of a holding org whose goal is a standing
// objective ("ship the Q4 roadmap") therefore pursues that objective every
// time its parent pings it. The fix is in the goal text: a child should be
// phrased for message-driven operation, e.g. "Handle requests from hq; when
// idle, complete." Task-specific runs go through org_start(org, task).
//
// The check is a deliberately simple, deterministic word test, and only
// ever produces a warning — it never blocks a save or a start. The goal is
// lowercased and split into words on every character outside an org name's
// alphabet ([a-z0-9_-]), so "hq:ceo's" yields "hq", "ceo", "s". A goal is
// message-driven when it has BOTH:
//
//  1. a request word: request(s), message(s), ask(s), inquiry/inquiries,
//     respond(s), reply/replies — it says it acts on incoming work; and
//  2. an end or source word: the parent org's name, idle, or complete(s) —
//     it names who the work comes from or says when to stop.
//
// Whole words only: "requestor", "incomplete", and "hqx" do not count.

var goalWordSplit = regexp.MustCompile(`[^a-z0-9_-]+`)

var goalRequestWords = map[string]bool{
	"request": true, "requests": true,
	"message": true, "messages": true,
	"ask": true, "asks": true,
	"inquiry": true, "inquiries": true,
	"respond": true, "responds": true,
	"reply": true, "replies": true,
}

var goalEndWords = map[string]bool{
	"idle": true, "complete": true, "completes": true,
}

// MessageDrivenGoal reports whether goal is phrased for message-driven
// operation as a child of the holding org parent (see the rules above).
func MessageDrivenGoal(goal, parent string) bool {
	parent = strings.ToLower(parent)
	var hasRequest, hasEnd bool
	for _, w := range goalWordSplit.Split(strings.ToLower(goal), -1) {
		if w == "" {
			continue
		}
		if goalRequestWords[w] {
			hasRequest = true
		}
		if goalEndWords[w] || (parent != "" && w == parent) {
			hasEnd = true
		}
	}
	return hasRequest && hasEnd
}

// ChildGoalWarnings returns one non-fatal warning per child org, listed by
// a holding org in docs, whose goal is not message-driven. Children missing
// from docs are skipped (ValidateProfileOrgs reports them). When only is
// not empty, just the warnings that concern that org — as the holding org
// or as the child — are returned. Output is sorted.
func ChildGoalWarnings(docs []*Doc, only string) []string {
	byName := make(map[string]*Doc, len(docs))
	for _, d := range docs {
		byName[d.Name] = d
	}
	var out []string
	for _, h := range docs {
		for _, c := range h.ChildOrgs {
			if only != "" && only != h.Name && only != c.Org {
				continue
			}
			child := byName[c.Org]
			if child == nil || MessageDrivenGoal(child.Goal, h.Name) {
				continue
			}
			out = append(out, fmt.Sprintf(
				"child org %q of holding org %q: goal %q is not phrased for messages — a message wakes a stopped org with a full run of its own goal, so phrase it like \"Handle requests from %s; when idle, complete.\" and use org_start(org, task) for task-specific runs",
				c.Org, h.Name, child.Goal, h.Name))
		}
	}
	sort.Strings(out)
	return out
}
