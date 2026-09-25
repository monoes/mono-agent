package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
	"github.com/monoes/mono-agent/internal/storage"
)

const richMetadata = `{"_source":{"source":"outlook","via":"service.outlook_mail","account":"me@x.com"},` +
	`"attachments":[{"filename":"cv.pdf","path":"/files/cv.pdf","size_bytes":12}],"connection_id":"outlook"}`

func TestWithMessageClassificationKeepsMetadata(t *testing.T) {
	c := messageClassification{Intent: "lead", IntentP: 0.9, ShouldReplyP: 0.8, Model: "m", At: "2026-09-25T00:00:00Z"}
	out, err := withMessageClassification(richMetadata, c)
	if err != nil {
		t.Fatal(err)
	}
	md := parseMessageMetadata(out)
	if md.Source.Source != "outlook" || md.Source.Account != "me@x.com" || len(md.Attachments) != 1 || md.Attachments[0].Path != "/files/cv.pdf" {
		t.Fatalf("existing metadata lost: %s", out)
	}
	if md.Classification == nil || *md.Classification != c {
		t.Fatalf("classification = %+v", md.Classification)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(out), &raw)
	if string(raw["connection_id"]) != `"outlook"` {
		t.Fatalf("unknown key connection_id not preserved: %s", out)
	}

	// Replacing a classification keeps everything else too.
	c2 := c
	c2.Intent = "spam"
	out2, err := withMessageClassification(out, c2)
	if err != nil || parseMessageMetadata(out2).Classification.Intent != "spam" || len(parseMessageMetadata(out2).Attachments) != 1 {
		t.Fatalf("reclassify: %v %s", err, out2)
	}

	if out, err := withMessageClassification("", c); err != nil || parseMessageMetadata(out).Classification == nil {
		t.Fatalf("empty metadata: %v %s", err, out)
	}
	if _, err := withMessageClassification(`["not","an","object"]`, c); err == nil {
		t.Fatal("non-object metadata must be refused, not overwritten")
	}
}

func seedInboxMessages(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	dbPath := newMessagesCLITestDB(t)
	classified, _ := withMessageClassification(`{"_source":{"source":"outlook"}}`,
		messageClassification{Intent: "personal", IntentP: 0.8, Model: "old", At: "2026-01-01T00:00:00Z"})
	seedPersonMessage(t, dbPath, "m-new", "E1", "inbound", "Bob Buyer", "sent", richMetadata)
	seedPersonMessage(t, dbPath, "m-out", "E2", "outbound", "me", "sent", `{}`)
	seedPersonMessage(t, dbPath, "m-done", "E3", "inbound", "Carol", "sent", classified)
	return dbPath
}

func runMessagesClassify(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, ProfileID: "default"}
	if len(args) > 0 && args[0] == "--json" {
		cfg.JSONOutput, args = true, args[1:]
	}
	var out bytes.Buffer
	cmd := newPeopleMessagesClassifyCmd(cfg)
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestMessagesClassifyDisabled(t *testing.T) {
	dbPath := seedInboxMessages(t)
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"intent": "lead"}))
	_, err := runMessagesClassify(t, dbPath)
	if err == nil || !strings.Contains(err.Error(), "jev enable inbox") {
		t.Fatalf("want an error naming `jev enable inbox`, got %v", err)
	}
	if srv.Calls() != 0 {
		t.Fatalf("disabled inbox surface made %d Jev calls", srv.Calls())
	}
	if md := parseMessageMetadata(fetchPersonMessage(t, dbPath, "m-new").Metadata); md.Classification != nil {
		t.Fatal("disabled surface stored a classification")
	}
}

func TestMessagesClassifyStoresSkipsAndReclassifies(t *testing.T) {
	dbPath := seedInboxMessages(t)
	enableJev(t, dbPath, "default", jevconf.Inbox)
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"intent": "lead", "should_reply": "0.83"}))

	if out, err := runMessagesClassify(t, dbPath); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if srv.Calls() != 1 {
		t.Fatalf("want 1 call (only the unclassified inbound message), got %d", srv.Calls())
	}
	req := srv.Requests()[0]
	state := req.State.(map[string]any)
	if state["sender_name"] != "Bob Buyer" || state["subject"] != "Hello" || state["untrusted_body"] != "orig body" {
		t.Fatalf("state = %v", state)
	}
	if len(req.Questions) != 2 || req.Questions["intent"].Type != "choice" || req.Questions["should_reply"].Type != "noul" {
		t.Fatalf("questions = %+v", req.Questions)
	}

	md := parseMessageMetadata(fetchPersonMessage(t, dbPath, "m-new").Metadata)
	if c := md.Classification; c == nil || c.Intent != "lead" || c.IntentP < 0.9 || c.ShouldReplyP != 0.83 || c.Model != "jev-test" || c.At == "" {
		t.Fatalf("classification = %+v", md.Classification)
	}
	if md.Source.Source != "outlook" || len(md.Attachments) != 1 {
		t.Fatalf("classify dropped existing metadata: %+v", md)
	}
	if old := parseMessageMetadata(fetchPersonMessage(t, dbPath, "m-done").Metadata).Classification; old.Intent != "personal" {
		t.Fatalf("already-classified message was touched: %+v", old)
	}
	if md := parseMessageMetadata(fetchPersonMessage(t, dbPath, "m-out").Metadata); md.Classification != nil {
		t.Fatal("outbound message was classified")
	}

	// A second run has nothing left to do.
	if _, err := runMessagesClassify(t, dbPath); err != nil || srv.Calls() != 1 {
		t.Fatalf("re-run: err=%v calls=%d", err, srv.Calls())
	}
	// --reclassify asks again for both inbound messages, one request each.
	if _, err := runMessagesClassify(t, dbPath, "--reclassify"); err != nil || srv.Calls() != 3 {
		t.Fatalf("reclassify: err=%v calls=%d", err, srv.Calls())
	}
	if c := parseMessageMetadata(fetchPersonMessage(t, dbPath, "m-done").Metadata).Classification; c.Intent != "lead" {
		t.Fatalf("--reclassify did not replace: %+v", c)
	}
}

func TestMessagesClassifyBelowThresholdStoresUnsure(t *testing.T) {
	dbPath := seedInboxMessages(t)
	enableJev(t, dbPath, "default", jevconf.Inbox)
	withJevDB(t, dbPath, func(db *storage.Database) {
		if err := jevconf.SetThreshold(db.DB, "default", jevconf.Inbox, 0.99); err != nil {
			t.Fatal(err)
		}
	})
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"intent": "lead"}))
	out, err := runMessagesClassify(t, dbPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"below_threshold": true`) {
		t.Fatalf("out=%s", out)
	}
	md := parseMessageMetadata(fetchPersonMessage(t, dbPath, "m-new").Metadata)
	if md.Classification == nil || !md.Classification.Unsure || md.Classification.Intent != "" {
		t.Fatalf("below-threshold answer should be stored as unsure with no intent: %+v", md.Classification)
	}
	calls := srv.Calls()
	// A second run must not pay for the same message again.
	if _, err := runMessagesClassify(t, dbPath, "--json"); err != nil {
		t.Fatal(err)
	}
	if srv.Calls() != calls {
		t.Fatalf("unsure message was classified again: %d → %d calls", calls, srv.Calls())
	}
}

func TestMessagesListIntentFilter(t *testing.T) {
	dbPath := seedInboxMessages(t)
	lead, _ := withMessageClassification(richMetadata, messageClassification{Intent: "lead", IntentP: 0.9})
	withJevDB(t, dbPath, func(db *storage.Database) {
		if _, err := db.DB.Exec(`UPDATE person_messages SET metadata = ? WHERE id = 'm-new'`, lead); err != nil {
			t.Fatal(err)
		}
	})

	stdout := captureStdout(t, func() {
		cmd := newPeopleMessagesListCmd(&globalConfig{DBPath: dbPath, ProfileID: "default", JSONOutput: true})
		cmd.SetArgs([]string{"p1", "--intent", "lead"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})
	var msgs []storage.PersonMessage
	if err := json.Unmarshal([]byte(stdout), &msgs); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	if len(msgs) != 1 || msgs[0].ID != "m-new" {
		t.Fatalf("--intent lead returned %+v", msgs)
	}

	cmd := newPeopleMessagesListCmd(&globalConfig{DBPath: dbPath, ProfileID: "default"})
	cmd.SetArgs([]string{"p1", "--intent", "bogus"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("an unknown --intent must be rejected")
	}
}

func TestParseAge(t *testing.T) {
	for in, ok := range map[string]bool{"": true, "12h": true, "7d": true, "1.5d": true, "x": false, "-1h": false} {
		if _, err := parseAge(in); (err == nil) != ok {
			t.Errorf("parseAge(%q) err=%v", in, err)
		}
	}
}
