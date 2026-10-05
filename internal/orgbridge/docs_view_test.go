package orgbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	recordedDir  = "../monomind/testdata/monomind-2.24.1"
	syntheticDir = "testdata/documents-synthetic"
	goldenView   = "../../wails-app/frontend/src/components/orgdesigner/__fixtures__/documents-view.json"
)

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// project lays a fixture store out the way monomind writes it.
func project(t *testing.T, org, run, events, notices, def string) string {
	t.Helper()
	root := t.TempDir()
	orgs := filepath.Join(root, ".monomind", "orgs")
	copyFile(t, events, filepath.Join(orgs, org, "docs", run, "events.jsonl"))
	if notices != "" {
		copyFile(t, notices, filepath.Join(orgs, org, "docs", run, "notices.jsonl"))
	}
	if def != "" {
		copyFile(t, def, filepath.Join(orgs, org+".json"))
	}
	return root
}

func docByID(t *testing.T, v DocView, id string) DocItem {
	t.Helper()
	for _, d := range v.Docs {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("document %s not in view", id)
	return DocItem{}
}

// The recorded 2.24.1 run: one note, read and accepted. The recorded org puts
// max_rework_rounds on the producing section, which never rejects, so no
// thread exists.
func TestRecordedRunRendersAcceptedNote(t *testing.T) {
	root := project(t, "sec", "run-20261005184408-kse5",
		filepath.Join(recordedDir, "doc-events.jsonl"), filepath.Join(recordedDir, "doc-notices.jsonl"),
		filepath.Join(recordedDir, "org-sec.json"))
	v, err := ReadDocView(root, "sec", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Run != "run-20261005184408-kse5" || v.Integrity != "" || v.Ignored != 0 || v.Seq != 3 {
		t.Fatalf("unexpected header: %+v", v)
	}
	d := docByID(t, v, "note-1")
	if d.Status != "accepted" || d.Producer != "writer" || d.Section != "drafting" ||
		len(d.Consumers) != 1 || d.Consumers[0] != "review" || len(d.Threads) != 0 || d.CapHit {
		t.Fatalf("note-1 = %+v", d)
	}
	if dec := d.Versions[0].Decisions["review"]; dec.Decision != "accept" || dec.By != "checker" {
		t.Fatalf("decision = %+v", dec)
	}
	if d.Versions[0].Reads["checker"] != 1 {
		t.Fatalf("reads = %+v", d.Versions[0].Reads)
	}
	if len(v.Sections) != 2 || v.Sections[0].Section != "drafting" || v.Sections[0].Types[0].Docs[0] != "note-1" {
		t.Fatalf("sections = %+v", v.Sections)
	}
}

func syntheticView(t *testing.T) DocView {
	t.Helper()
	root := project(t, "rework", "run-syn", filepath.Join(syntheticDir, "events.jsonl"),
		filepath.Join(syntheticDir, "notices.jsonl"), filepath.Join(syntheticDir, "org-rework.json"))
	v, err := ReadDocView(root, "rework", "run-syn")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSyntheticReworkCycle(t *testing.T) {
	v := syntheticView(t)
	if v.Integrity != "" {
		t.Fatalf("synthetic chain broken: %s", v.Integrity)
	}
	d := docByID(t, v, "note-1")
	if d.Status != "accepted" || d.Round != 1 || d.Cap != 2 || d.CapHit || d.Frozen {
		t.Fatalf("note-1 = %+v", d)
	}
	v1, v2 := d.Versions[0], d.Versions[1]
	if v1.Status != "rejected" || v1.SupersededBy != 2 || v2.Supersedes != 1 || v2.Status != "accepted" {
		t.Fatalf("lineage = %+v / %+v", v1, v2)
	}
	if v1.Decisions["review"].Reason == "" || v1.Decisions["review"].RelayedAt == "" {
		t.Fatalf("rejection lost its reason or relay: %+v", v1.Decisions["review"])
	}
	if len(d.Deliverables) != 1 || d.Deliverables[0] != "out/note.json" {
		t.Fatalf("deliverables = %v", d.Deliverables)
	}
}

func TestSyntheticExhaustedCapIsFrozenAndBadged(t *testing.T) {
	v := syntheticView(t)
	d := docByID(t, v, "note-2")
	if !d.CapHit || !d.Frozen || d.Round != 2 || d.Cap != 2 || d.Status != "rejected" {
		t.Fatalf("note-2 = %+v", d)
	}
	if th := d.Threads[0]; !th.Exhausted || !th.Frozen || th.Consumer != "review" {
		t.Fatalf("thread = %+v", th)
	}
	if v.Summary.CapHit != 1 {
		t.Fatalf("summary = %+v", v.Summary)
	}
}

func TestSyntheticRootOverrideThawsTheThread(t *testing.T) {
	d := docByID(t, syntheticView(t), "note-3")
	if d.Status != "accepted" || d.CapHit || d.Frozen || d.Round != 1 {
		t.Fatalf("note-3 = %+v", d)
	}
	if dec := d.Versions[1].Decisions["review"]; !dec.Override || dec.By != "lead" {
		t.Fatalf("override decision = %+v", dec)
	}
}

func TestSyntheticPendingAndSummary(t *testing.T) {
	v := syntheticView(t)
	d := docByID(t, v, "note-4")
	if d.Status != "pending" || len(d.Versions[0].WaitingOn) != 1 {
		t.Fatalf("note-4 = %+v", d)
	}
	if v.Summary.Documents != 4 || v.Summary.Pending != 1 || v.Summary.Accepted != 2 || v.Summary.Rejected != 1 {
		t.Fatalf("summary = %+v", v.Summary)
	}
	// the refused event is a known kind that changes no document
	if v.Ignored != 0 {
		t.Fatalf("ignored = %d", v.Ignored)
	}
}

// Without the org definition the cap is recovered from the delivered
// rework-exhausted notice key.
func TestCapFromExhaustedNoticeWhenDefinitionIsGone(t *testing.T) {
	root := project(t, "rework", "run-syn", filepath.Join(syntheticDir, "events.jsonl"),
		filepath.Join(syntheticDir, "notices.jsonl"), "")
	v, err := ReadDocView(root, "rework", "")
	if err != nil {
		t.Fatal(err)
	}
	if d := docByID(t, v, "note-2"); !d.CapHit || d.Cap != 2 {
		t.Fatalf("note-2 = %+v", d)
	}
}

func TestUnknownAndMalformedEventsNeverFail(t *testing.T) {
	v := BuildDocView(DocInput{Org: "o", Run: "r", Events: []DocEvent{
		{Seq: 1, Type: "section_status"},
		{Seq: 2, Type: "loops", Doc: "x"},
		{Seq: 3, Type: "decided", Doc: "ghost", Version: 4, Consumer: "c", Decision: "accept"},
		{Seq: 4, Type: "read", Doc: "ghost", Version: 1},
		{Seq: 5, Type: "published", Doc: "note-1", DocType: "note", Section: "s", Version: 1, By: "w", Consumers: []string{"r"}},
		{Seq: 6, Type: "published", Doc: "note-1", DocType: "note", Section: "s", Version: 7, By: "w"},
		{Seq: 7, Type: "decided", Doc: "note-1", Version: 1, Consumer: "r", Decision: "maybe"},
		{Seq: 8, Type: "future-kind-from-2.99"},
	}})
	if len(v.Docs) != 1 || v.Docs[0].Status != "pending" || v.Ignored != 7 {
		t.Fatalf("view = %+v", v)
	}
}

func TestBrokenChainStopsAtTheLastGoodEvent(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(syntheticDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	lines[4] = strings.Replace(lines[4], `"by":"checker"`, `"by":"mallory"`, 1) // an edited line breaks the next prev
	dir := t.TempDir()
	p := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, why := readDocEvents(p)
	if len(evs) != 5 || !strings.Contains(why, "does not chain") {
		t.Fatalf("got %d events, %q", len(evs), why)
	}
	// a torn final line is not corruption
	torn := filepath.Join(dir, "torn.jsonl")
	if err := os.WriteFile(torn, []byte(strings.Join(lines[:3], "\n")+"\n{\"seq\":4,\"pr"), 0o644); err != nil {
		t.Fatal(err)
	}
	if evs, why := readDocEvents(torn); len(evs) != 3 || why != "" {
		t.Fatalf("torn: %d events, %q", len(evs), why)
	}
}

func TestReadDocViewValidatesNamesAndToleratesNoStore(t *testing.T) {
	root := t.TempDir()
	if _, err := ReadDocView(root, "../x", ""); err == nil {
		t.Fatal("expected an invalid org name error")
	}
	if _, err := ReadDocView(root, "o", "../../etc"); err == nil {
		t.Fatal("expected an invalid run id error")
	}
	v, err := ReadDocView(root, "o", "")
	if err != nil || len(v.Docs) != 0 || v.Runs == nil {
		t.Fatalf("empty view = %+v, %v", v, err)
	}
}

// The wire view the panel's tests render is generated from the synthetic run
// plus the recorded one, so Go stays the single source of the shape.
// Regenerate with: UPDATE_GOLDEN=1 go test ./internal/orgbridge -run Golden
func TestGoldenViewMatchesFrontendFixture(t *testing.T) {
	got, err := json.MarshalIndent(syntheticView(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenView, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenView)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Fatalf("%s is stale; regenerate with UPDATE_GOLDEN=1", goldenView)
	}
}
