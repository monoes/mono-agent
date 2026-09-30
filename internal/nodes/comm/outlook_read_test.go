package comm

import (
	"bufio"
	"context"
	"encoding/base64"
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
	// envelopes are raw ENVELOPE values per message, {n} literals
	// included; a missing one gets a plain envelope.
	envelopes []string
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
				env := fmt.Sprintf(`("Mon, 1 Sep 2026 10:00:00 +0000" "Subject %d" (("Ann" NIL "ann" "example.com")) NIL NIL NIL NIL NIL NIL "<m%d@example.com>")`, seq, seq)
				if i < len(f.envelopes) {
					env = f.envelopes[i]
				}
				// A literal's bytes follow its {n} line; the response
				// continues right after them.
				fmt.Fprintf(conn, "* %d FETCH (FLAGS (%s) ENVELOPE %s BODY[TEXT]<0> {%d}\r\n%s)\r\n", seq, flags, env, len(body), body)
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

// fetchFrom runs fetchOutlookMail against f and returns the items oldest
// first (the node returns newest first).
func fetchFrom(t *testing.T, f *fakeIMAP) []map[string]interface{} {
	t.Helper()
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
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}

// TestFetchOutlookMailParsesEnvelopes: envelopes parse as IMAP
// s-expressions and encoded words decode (#292).
func TestFetchOutlookMailParsesEnvelopes(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("Grüße aus München"))
	literal := `He said "hi" (then left) \ ok`
	envs := []struct {
		env                            string
		date, subject, from, messageID string
	}{
		{ // parentheses and escaped quotes inside quoted strings
			`("Tue, 2 Sep 2026 09:00:00 +0000" "Re: (draft) \"plan\" v2 :)" (("O'Brien, Pat (Ops)" NIL "pat" "example.com")) NIL NIL NIL NIL NIL NIL "<a(1)@example.com>")`,
			"Tue, 2 Sep 2026 09:00:00 +0000", `Re: (draft) "plan" v2 :)`, "O'Brien, Pat (Ops) <pat@example.com>", "<a(1)@example.com>",
		},
		{ // B-encoded UTF-8 subject, Q-encoded display name
			`(NIL "=?UTF-8?B?` + b64 + `?=" (("=?utf-8?Q?J=C3=BCrgen_M=C3=BCller?=" NIL "juergen" "example.de")) NIL NIL NIL NIL NIL NIL "<b@example.de>")`,
			"", "Grüße aus München", "Jürgen Müller <juergen@example.de>", "<b@example.de>",
		},
		{ // Q-encoded UTF-8 and a charset the stdlib doesn't know
			`(NIL "=?utf-8?Q?Caf=C3=A9_ouvert?= / =?windows-1252?Q?caf=E9?=" ((NIL NIL "x" "example.fr")) NIL NIL NIL NIL NIL NIL NIL)`,
			"", "Café ouvert / café", "x@example.fr", "",
		},
		{ // a subject sent as a literal, with quotes, parens and a backslash
			fmt.Sprintf("(NIL {%d}\r\n%s ((\"Lit\" NIL \"lit\" \"example.com\")) NIL NIL NIL NIL NIL NIL \"<c@example.com>\")", len(literal), literal),
			"", literal, "Lit <lit@example.com>", "<c@example.com>",
		},
		{ // NIL everywhere
			`(NIL NIL NIL NIL NIL NIL NIL NIL NIL NIL)`,
			"", "", "", "",
		},
	}
	f := &fakeIMAP{seen: map[int]bool{}}
	for i, e := range envs {
		f.envelopes = append(f.envelopes, e.env)
		f.bodies = append(f.bodies, fmt.Sprintf("body %d", i+1))
	}

	items := fetchFrom(t, f)
	if len(items) != len(envs) {
		t.Fatalf("got %d items, want %d: %v", len(items), len(envs), items)
	}
	for i, e := range envs {
		want := map[string]interface{}{
			"date": e.date, "subject": e.subject, "from": e.from, "message_id": e.messageID,
			"body": fmt.Sprintf("body %d", i+1), "read": false,
		}
		for k, v := range want {
			if items[i][k] != v {
				t.Errorf("message %d %s = %q, want %q", i+1, k, items[i][k], v)
			}
		}
	}
}

// A malformed or truncated response is skipped, never a panic.
func TestParseFetchResponseRejectsMalformed(t *testing.T) {
	for _, text := range []string{
		"", "*", "* 1 FETCH", "* 1 FETCH (", `* 1 FETCH (ENVELOPE ("unterminated`,
		"* 1 FETCH (ENVELOPE ({5}", "* 1 FETCH )", "* x FETCH ()", "* 1 EXISTS",
		"* 1 FETCH (ENVELOPE ({99999999999999999999}",
	} {
		if _, ok := parseFetchResponse(imapResponse{text: text}); ok {
			t.Errorf("%q parsed as a FETCH response", text)
		}
	}
}
