package comm

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeIMAP is a minimal IMAP server for fetchOutlookMail. It applies the
// RFC 3501 rules that change mailbox state: a FETCH of BODY[...] (not
// BODY.PEEK) in a SELECTed mailbox sets \Seen, and so does STORE.
type fakeIMAP struct {
	mu       sync.Mutex
	commands []string
	seen     map[int]bool // server-side \Seen per message
	bodies   []string
}

func (f *fakeIMAP) serve(t *testing.T, conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := func(format string, args ...interface{}) { fmt.Fprintf(conn, format+"\r\n", args...) }
	w("* OK fake IMAP ready")
	readOnly := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.commands = append(f.commands, line)
		f.mu.Unlock()
		tag, rest, _ := strings.Cut(line, " ")
		verb, args, _ := strings.Cut(rest, " ")
		switch strings.ToUpper(verb) {
		case "LOGIN":
			w("%s OK LOGIN completed", tag)
		case "SELECT", "EXAMINE":
			readOnly = strings.EqualFold(verb, "EXAMINE")
			w("* %d EXISTS", len(f.bodies))
			w("%s OK [READ-%s] done", tag, map[bool]string{true: "ONLY", false: "WRITE"}[readOnly])
		case "STORE":
			f.mu.Lock()
			for i := range f.bodies {
				f.seen[i+1] = true
			}
			f.mu.Unlock()
			w("%s OK STORE completed", tag)
		case "FETCH":
			marks := !readOnly && strings.Contains(args, "BODY[")
			for i, body := range f.bodies {
				seq := i + 1
				f.mu.Lock()
				if marks {
					f.seen[seq] = true
				}
				flags := ""
				if f.seen[seq] {
					flags = `\Seen`
				}
				f.mu.Unlock()
				w(`* %d FETCH (FLAGS (%s) ENVELOPE ("Mon, 1 Sep 2026 10:00:00 +0000" "Subject %d" (("Ann" NIL "ann" "example.com")) NIL NIL NIL NIL NIL NIL "<m%d@example.com>") BODY[TEXT]<0> {%d}`, seq, flags, seq, seq, len(body))
				fmt.Fprint(conn, body)
				w("")
				w(")")
			}
			w("%s OK FETCH completed", tag)
		case "LOGOUT":
			w("* BYE")
			w("%s OK LOGOUT completed", tag)
			return
		default:
			w("%s BAD unknown command", tag)
		}
	}
}

// TestFetchOutlookMailDoesNotMarkSeen: reading mail leaves unread messages
// unread on the server (#290): EXAMINE, BODY.PEEK, and no STORE.
func TestFetchOutlookMailDoesNotMarkSeen(t *testing.T) {
	f := &fakeIMAP{seen: map[int]bool{2: true}, bodies: []string{"first body", "second body"}}
	prev := dialOutlookIMAP
	t.Cleanup(func() { dialOutlookIMAP = prev })
	done := make(chan struct{})
	dialOutlookIMAP = func(ctx context.Context, host string, port int) (net.Conn, error) {
		client, server := net.Pipe()
		go func() { defer close(done); f.serve(t, server) }()
		return client, nil
	}

	items, err := fetchOutlookMail(context.Background(), "imap.test", 993, "me@example.com", "pw", "INBOX", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	<-done

	if f.seen[1] {
		t.Error("message 1 was marked \\Seen by the fetch")
	}
	for _, c := range f.commands {
		up := strings.ToUpper(c)
		if strings.Contains(up, " STORE ") || strings.Contains(up, " SELECT ") {
			t.Errorf("command %q can change mailbox state", c)
		}
		if strings.Contains(up, " FETCH ") && !strings.Contains(up, "BODY.PEEK[") {
			t.Errorf("FETCH without BODY.PEEK: %q", c)
		}
	}

	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	// Newest first; each keeps its own read state.
	if items[0]["body"] != "second body" || items[0]["read"] != true ||
		items[1]["body"] != "first body" || items[1]["read"] != false {
		t.Fatalf("items = %v", items)
	}
	// The envelope fills date, subject, from and message_id.
	for i, want := range []map[string]interface{}{
		{"subject": "Subject 2", "message_id": "<m2@example.com>"},
		{"subject": "Subject 1", "message_id": "<m1@example.com>"},
	} {
		want["date"] = "Mon, 1 Sep 2026 10:00:00 +0000"
		want["from"] = "Ann <ann@example.com>"
		for k, v := range want {
			if items[i][k] != v {
				t.Errorf("item %d %s = %q, want %q", i, k, items[i][k], v)
			}
		}
	}
}
