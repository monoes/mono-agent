package orgbridge

import (
	"fmt"
	"sort"
	"strings"
)

// The documents view-model of one org run: what the Documents panel renders.
//
// monomind (2.24) keeps a run's documents in an event-sourced store,
// `.monomind/orgs/<org>/docs/<run>/events.jsonl`. Status is never stored
// there; it is derived by replaying the events. This file replays them the
// same way monomind's own reducer does (orgrt/documents/state.ts applyEvent,
// versionStatus, rework.ts reworkThreads) so the panel and the runtime agree,
// and adds the producer->consumer, lineage and cap facts the panel needs.
//
// The reducer is tolerant by design: an unknown event type, an event about a
// document it has not seen, or a malformed field is skipped and counted in
// Ignored, never a failure. A broken hash chain stops the replay at the last
// good event (as monomind does) and says so in Integrity.

// DocEvent is one line of events.jsonl. Only the fields the view uses.
type DocEvent struct {
	Seq        int      `json:"seq"`
	At         string   `json:"at"`
	Type       string   `json:"type"`
	Doc        string   `json:"doc"`
	DocType    string   `json:"doc_type"`
	Section    string   `json:"section"`
	Version    int      `json:"version"`
	By         string   `json:"by"`
	Supersedes int      `json:"supersedes"`
	Note       string   `json:"note"`
	Bytes      int      `json:"bytes"`
	Consumers  []string `json:"consumers"`
	Consumer   string   `json:"consumer"`
	Decision   string   `json:"decision"`
	Reason     string   `json:"reason"`
	Override   bool     `json:"override"`
	Code       string   `json:"code"`
}

// DocDecision is one consuming section's standing decision on a version.
type DocDecision struct {
	Decision string `json:"decision"` // accept | reject
	By       string `json:"by"`
	At       string `json:"at"`
	Reason   string `json:"reason,omitempty"`
	// Override: the root replaced a spent review cycle's rejection.
	Override bool `json:"override,omitempty"`
	// RelayedAt: when the rejection was delivered to the producer.
	RelayedAt string `json:"relayed_at,omitempty"`
}

// DocVersion is one published version of a document.
type DocVersion struct {
	Version      int                    `json:"version"`
	Status       string                 `json:"status"` // pending | accepted | rejected | superseded
	By           string                 `json:"by"`
	At           string                 `json:"at"`
	Bytes        int                    `json:"bytes"`
	Supersedes   int                    `json:"supersedes,omitempty"`
	SupersededBy int                    `json:"superseded_by,omitempty"`
	Note         string                 `json:"note,omitempty"`
	Consumers    []string               `json:"consumers"`
	WaitingOn    []string               `json:"waiting_on"`
	Decisions    map[string]DocDecision `json:"decisions"`
	Reads        map[string]int         `json:"reads"`
}

// DocThread is one (document, consuming section) review cycle that has at
// least one rejection and a cap. Round N of Cap is Rounds of Cap.
type DocThread struct {
	Consumer  string `json:"consumer"`
	Rounds    int    `json:"rounds"`
	Cap       int    `json:"cap"`
	Exhausted bool   `json:"exhausted"`
	// Frozen: exhausted and the head is not accepted, so a revision is refused
	// until the root decides it or raises the cap.
	Frozen bool `json:"frozen"`
}

// DocItem is one document with its versions and review state.
type DocItem struct {
	ID        string       `json:"id"`
	Type      string       `json:"type"`
	Section   string       `json:"section"`
	Producer  string       `json:"producer"`
	Consumers []string     `json:"consumers"` // consuming sections of the head version
	Status    string       `json:"status"`    // the head version's status
	Head      int          `json:"head"`
	Round     int          `json:"round"`         // highest rounds over the document's threads
	Cap       int          `json:"cap,omitempty"` // that thread's cap, 0 when none
	CapHit    bool         `json:"cap_hit"`
	Frozen    bool         `json:"frozen"`
	Threads   []DocThread  `json:"threads"`
	Versions  []DocVersion `json:"versions"`
	// Deliverables: the files the type's contract ties to the document.
	Deliverables []string `json:"deliverables"`
	UpdatedAt    string   `json:"updated_at"`
}

// DocSectionGroup lists the documents of one producing section, by type.
type DocSectionGroup struct {
	Section string         `json:"section"`
	Cap     int            `json:"cap,omitempty"` // the section's own max_rework_rounds as a consumer
	Types   []DocTypeGroup `json:"types"`
	Counts  map[string]int `json:"counts"`
}

// DocTypeGroup lists the documents of one type inside a section.
type DocTypeGroup struct {
	Type         string   `json:"type"`
	Docs         []string `json:"docs"`
	Deliverables []string `json:"deliverables"`
}

// DocView is what `org documents` prints and the Documents panel renders.
type DocView struct {
	V    int      `json:"v"`
	Org  string   `json:"org"`
	Run  string   `json:"run"`
	Runs []string `json:"runs"`
	Seq  int      `json:"seq"`
	// Integrity is empty when the log chained cleanly to its end.
	Integrity string            `json:"integrity,omitempty"`
	Ignored   int               `json:"ignored"`
	Caps      map[string]int    `json:"caps"`
	Sections  []DocSectionGroup `json:"sections"`
	Docs      []DocItem         `json:"docs"`
	// Totals for badges: documents, rejections, exhausted threads.
	Summary DocSummary `json:"summary"`
}

// DocSummary is the roll-up shown in the panel header.
type DocSummary struct {
	Documents int `json:"documents"`
	Pending   int `json:"pending"`
	Accepted  int `json:"accepted"`
	Rejected  int `json:"rejected"`
	Reworking int `json:"reworking"` // documents with a rejection under a cap, not exhausted, head not accepted
	CapHit    int `json:"cap_hit"`
}

// DocInput is everything the view is built from. Any field may be empty.
type DocInput struct {
	Org, Run  string
	Runs      []string
	Events    []DocEvent
	Integrity string
	// Caps: max_rework_rounds per consuming section (from the org definition).
	Caps map[string]int
	// Deliverables: the contract's deliverable_files per document type.
	Deliverables map[string][]string
	// SectionOrder lists the org definition's sections so empty ones show.
	SectionOrder []string
	// Notices: journal keys that were delivered (notices.jsonl) with the time.
	Delivered map[string]string
}

type docState struct {
	item     *DocItem
	versions []*DocVersion
}

// BuildDocView replays in.Events into the view-model. It never fails.
func BuildDocView(in DocInput) DocView {
	caps := map[string]int{}
	for k, v := range in.Caps {
		if v > 0 {
			caps[k] = v
		}
	}
	for k, v := range capsFromNoticeKeys(in.Delivered) { // definition missing or reloaded: the notice names its cap
		if _, ok := caps[k]; !ok {
			caps[k] = v
		}
	}
	view := DocView{V: 1, Org: in.Org, Run: in.Run, Runs: nonNilStrings(in.Runs), Caps: caps,
		Integrity: in.Integrity, Docs: []DocItem{}, Sections: []DocSectionGroup{}}

	docs := map[string]*docState{}
	var order []string
	for _, e := range in.Events {
		if e.Seq > view.Seq {
			view.Seq = e.Seq
		}
		switch e.Type {
		case "published":
			if e.Doc == "" || e.Version < 1 {
				view.Ignored++
				continue
			}
			d := docs[e.Doc]
			if d == nil {
				if e.Supersedes != 0 {
					view.Ignored++
					continue
				}
				d = &docState{item: &DocItem{ID: e.Doc, Type: e.DocType, Section: e.Section, Producer: e.By}}
				docs[e.Doc] = d
				order = append(order, e.Doc)
			} else if e.Version != len(d.versions)+1 {
				view.Ignored++
				continue
			}
			v := &DocVersion{Version: e.Version, By: e.By, At: e.At, Bytes: e.Bytes, Note: e.Note,
				Supersedes: e.Supersedes, Consumers: nonNilStrings(e.Consumers),
				Decisions: map[string]DocDecision{}, Reads: map[string]int{}}
			if e.Supersedes > 0 && e.Supersedes <= len(d.versions) {
				d.versions[e.Supersedes-1].SupersededBy = e.Version
			}
			d.versions = append(d.versions, v)
			d.item.UpdatedAt = e.At
		case "decided":
			v := versionOf(docs, e.Doc, e.Version)
			if v == nil || (e.Decision != "accept" && e.Decision != "reject") || !contains(v.Consumers, e.Consumer) {
				view.Ignored++
				continue
			}
			dec := DocDecision{Decision: e.Decision, By: e.By, At: e.At, Reason: e.Reason, Override: e.Override}
			if e.Decision == "reject" {
				dec.RelayedAt = in.Delivered[fmt.Sprintf("r:%d:producer", e.Seq)]
			}
			v.Decisions[e.Consumer] = dec // an override replaces the standing rejection
			docs[e.Doc].item.UpdatedAt = e.At
		case "read":
			v := versionOf(docs, e.Doc, e.Version)
			if v == nil {
				view.Ignored++
				continue
			}
			v.Reads[e.By]++
		case "refused":
			// A refused publish or decision changes no document.
		default:
			view.Ignored++ // an event kind from a newer monomind
		}
	}

	sum := &view.Summary
	for _, id := range order {
		d := docs[id]
		finishDoc(d, caps, in.Deliverables)
		view.Docs = append(view.Docs, *d.item)
		sum.Documents++
		switch d.item.Status {
		case "pending":
			sum.Pending++
		case "accepted":
			sum.Accepted++
		case "rejected":
			sum.Rejected++
		}
		if d.item.CapHit {
			sum.CapHit++
		} else if d.item.Round > 0 && d.item.Status != "accepted" {
			sum.Reworking++
		}
	}
	view.Sections = groupSections(view.Docs, in.SectionOrder, caps, in.Deliverables)
	return view
}

func versionOf(docs map[string]*docState, id string, version int) *DocVersion {
	d := docs[id]
	if d == nil || version < 1 || version > len(d.versions) {
		return nil
	}
	return d.versions[version-1]
}

// finishDoc derives statuses, threads and the head summary (state.ts
// versionStatus / waitingOn, rework.ts reworkThreads).
func finishDoc(d *docState, caps map[string]int, deliverables map[string][]string) {
	it := d.item
	head := d.versions[len(d.versions)-1]
	for _, v := range d.versions {
		v.Status = versionStatus(v, v == head)
		v.WaitingOn = []string{}
		if v.Status == "pending" {
			for _, c := range v.Consumers {
				if _, ok := v.Decisions[c]; !ok {
					v.WaitingOn = append(v.WaitingOn, c)
				}
			}
		}
		it.Versions = append(it.Versions, *v)
	}
	it.Head, it.Status = head.Version, head.Status
	it.Consumers = head.Consumers
	it.Deliverables = nonNilStrings(deliverables[it.Type])

	seen := map[string]bool{}
	var consumers []string
	for _, v := range d.versions {
		for _, c := range v.Consumers {
			if !seen[c] {
				seen[c] = true
				consumers = append(consumers, c)
			}
		}
	}
	sort.Strings(consumers)
	it.Threads = []DocThread{}
	for _, c := range consumers {
		cap, ok := caps[c]
		if !ok {
			continue
		}
		rounds := 0
		for _, v := range d.versions {
			if dec, ok := v.Decisions[c]; ok && dec.Decision == "reject" {
				rounds++
			}
		}
		if rounds == 0 {
			continue
		}
		t := DocThread{Consumer: c, Rounds: rounds, Cap: cap, Exhausted: rounds >= cap}
		t.Frozen = t.Exhausted && head.Status != "accepted"
		it.Threads = append(it.Threads, t)
		if rounds > it.Round || (rounds == it.Round && t.Exhausted) {
			it.Round, it.Cap = rounds, cap
		}
		it.CapHit = it.CapHit || t.Exhausted
		it.Frozen = it.Frozen || t.Frozen
	}
}

// versionStatus: rejected (any consumer rejected), accepted (every pinned
// consumer accepted), else pending when it is the head, superseded when not.
func versionStatus(v *DocVersion, isHead bool) string {
	allAccepted := len(v.Consumers) > 0
	for _, dec := range v.Decisions {
		if dec.Decision == "reject" {
			return "rejected"
		}
	}
	for _, c := range v.Consumers {
		if v.Decisions[c].Decision != "accept" {
			allAccepted = false
		}
	}
	if allAccepted {
		return "accepted"
	}
	if isHead {
		return "pending"
	}
	return "superseded"
}

func groupSections(docs []DocItem, defOrder []string, caps map[string]int, deliverables map[string][]string) []DocSectionGroup {
	idx := map[string]*DocSectionGroup{}
	var names []string
	add := func(name string) *DocSectionGroup {
		g := idx[name]
		if g == nil {
			g = &DocSectionGroup{Section: name, Cap: caps[name], Types: []DocTypeGroup{}, Counts: map[string]int{}}
			idx[name] = g
			names = append(names, name)
		}
		return g
	}
	for _, n := range defOrder {
		add(n)
	}
	for _, d := range docs {
		g := add(d.Section)
		g.Counts[d.Status]++
		var tg *DocTypeGroup
		for i := range g.Types {
			if g.Types[i].Type == d.Type {
				tg = &g.Types[i]
			}
		}
		if tg == nil {
			g.Types = append(g.Types, DocTypeGroup{Type: d.Type, Docs: []string{}, Deliverables: nonNilStrings(deliverables[d.Type])})
			tg = &g.Types[len(g.Types)-1]
		}
		tg.Docs = append(tg.Docs, d.ID)
	}
	out := make([]DocSectionGroup, 0, len(names))
	for _, n := range names {
		out = append(out, *idx[n])
	}
	return out
}

// capsFromNoticeKeys reads the cap out of delivered rework-exhausted notice
// keys: `x:<doc>|<consumer>|<cap>@<seq>:<to>` (rework.ts deriveReworkNotices).
func capsFromNoticeKeys(delivered map[string]string) map[string]int {
	out := map[string]int{}
	for k := range delivered {
		rest, ok := strings.CutPrefix(k, "x:")
		if !ok {
			continue
		}
		head, _, ok := strings.Cut(rest, "@")
		if !ok {
			continue
		}
		parts := strings.Split(head, "|")
		if len(parts) != 3 {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(parts[2], "%d", &n); err == nil && n > 0 {
			out[parts[1]] = n
		}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func nonNilStrings(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
