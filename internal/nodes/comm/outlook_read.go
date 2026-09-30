package comm

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/workflow"
)

// OutlookReadNode fetches emails from Outlook / Hotmail via IMAP.
// Type: "comm.outlook_read"
//
// Config fields:
//
//	"email"       (string, required): Outlook/Hotmail address (also the IMAP username)
//	"password"    (string, required): Account password or app password
//	"mailbox"     (string, default "INBOX"): mailbox folder to read
//	"limit"       (int, default 10): max messages to fetch (most recent first)
//	"unread_only" (bool, default false): only return unseen messages
//
// Uses Outlook IMAP: outlook.office365.com:993 with TLS.
// Returns each message as an Item with fields:
// "subject", "from", "date", "body", "message_id", "read"
type OutlookReadNode struct{}

func (n *OutlookReadNode) Type() string { return "comm.outlook_read" }

func (n *OutlookReadNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	const imapHost = "outlook.office365.com"
	const imapPort = 993

	email, _ := config["email"].(string)
	if email == "" {
		return nil, fmt.Errorf("comm.outlook_read: email is required")
	}

	password, _ := config["password"].(string)
	if password == "" {
		password, _ = config["app_password"].(string)
	}
	if password == "" {
		return nil, fmt.Errorf("comm.outlook_read: password is required")
	}

	mailbox := "INBOX"
	if mb, ok := config["mailbox"].(string); ok && mb != "" {
		mailbox = mb
	}

	limit := 10
	switch v := config["limit"].(type) {
	case int:
		limit = v
	case float64:
		limit = int(v)
	}
	if limit <= 0 {
		limit = 10
	}

	unreadOnly := false
	if u, ok := config["unread_only"].(bool); ok {
		unreadOnly = u
	}

	items, err := fetchOutlookMail(ctx, imapHost, imapPort, email, password, mailbox, limit, unreadOnly)
	if err != nil {
		return nil, fmt.Errorf("comm.outlook_read: %w", err)
	}

	out := make([]workflow.Item, len(items))
	for i, m := range items {
		out[i] = workflow.NewItem(m)
	}
	return []workflow.NodeOutput{{Handle: "main", Items: out}}, nil
}

// dialOutlookIMAP opens the TLS connection to the IMAP server; tests swap
// it for a fake server.
var dialOutlookIMAP = func(ctx context.Context, host string, port int) (net.Conn, error) {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 15 * time.Second},
		Config:    &tls.Config{ServerName: host},
	}
	return dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
}

// fetchOutlookMail performs a minimal IMAP fetch using raw text protocol.
// This avoids a third-party IMAP library dependency while still being functional
// for simple cases. For production use, replace with go-imap or similar.
// It only reads: the mailbox is opened with EXAMINE (read-only) and bodies
// are fetched with BODY.PEEK, so no message gets the \Seen flag.
func fetchOutlookMail(ctx context.Context, host string, port int, username, password, mailbox string, limit int, unreadOnly bool) ([]map[string]interface{}, error) {
	conn, err := dialOutlookIMAP(ctx, host, port)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	tc := textproto.NewConn(conn)
	defer tc.Close() // also closes the underlying conn

	// Close the connection when the workflow context is cancelled so blocked
	// reads unblock instead of hanging the node goroutine indefinitely.
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-ctxDone:
		}
	}()

	readLine := func() (string, error) {
		// Idle read deadline: a stalled server (e.g. terminating tag never
		// arrives) fails the read instead of blocking forever.
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, err := tc.ReadLine()
		return line, err
	}
	send := func(tag, cmd string) error {
		_, err := fmt.Fprintf(conn, "%s %s\r\n", tag, cmd)
		return err
	}
	expectOK := func(tag string) error {
		for {
			line, err := readLine()
			if err != nil {
				return err
			}
			if strings.HasPrefix(line, tag+" OK") {
				return nil
			}
			if strings.HasPrefix(line, tag+" NO") || strings.HasPrefix(line, tag+" BAD") {
				return fmt.Errorf("IMAP error: %s", line)
			}
		}
	}

	// Read server greeting
	if _, err := readLine(); err != nil {
		return nil, fmt.Errorf("greeting: %w", err)
	}

	// LOGIN
	if err := send("A1", fmt.Sprintf("LOGIN %q %q", username, password)); err != nil {
		return nil, fmt.Errorf("login send: %w", err)
	}
	if err := expectOK("A1"); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}

	// EXAMINE mailbox (SELECT, but read-only)
	if err := send("A2", fmt.Sprintf("EXAMINE %q", mailbox)); err != nil {
		return nil, err
	}
	var totalMessages int
	for {
		line, err := readLine()
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "A2 OK") {
			break
		}
		if strings.HasPrefix(line, "A2 NO") || strings.HasPrefix(line, "A2 BAD") {
			return nil, fmt.Errorf("EXAMINE: %s", line)
		}
		// Parse EXISTS count
		var n int
		if _, err := fmt.Sscanf(line, "* %d EXISTS", &n); err == nil {
			totalMessages = n
		}
	}

	if totalMessages == 0 {
		return []map[string]interface{}{}, nil
	}

	// Determine message range (most recent first)
	start := totalMessages - limit + 1
	if start < 1 {
		start = 1
	}
	searchSpec := fmt.Sprintf("%d:%d", start, totalMessages)
	if unreadOnly {
		// For unread-only, search all UNSEEN — simplified: fetch range and filter by \Seen flag
		searchSpec = fmt.Sprintf("%d:*", start)
	}

	// FETCH envelope and body; the response still says BODY[TEXT]
	fetchCmd := fmt.Sprintf("FETCH %s (FLAGS ENVELOPE BODY.PEEK[TEXT]<0.2048>)", searchSpec)
	if err := send("A3", fetchCmd); err != nil {
		return nil, err
	}

	var results []map[string]interface{}
	for {
		resp, err := readIMAPResponse(readLine, tc.R)
		if err != nil {
			break
		}
		if strings.HasPrefix(resp.text, "A3 OK") {
			break
		}
		if strings.HasPrefix(resp.text, "A3 NO") || strings.HasPrefix(resp.text, "A3 BAD") {
			return nil, fmt.Errorf("FETCH: %s", resp.text)
		}
		attrs, ok := parseFetchResponse(resp)
		if !ok {
			continue
		}

		msg := map[string]interface{}{"read": false}
		flags, _ := attrs["FLAGS"].([]interface{})
		for _, f := range flags {
			if strings.EqualFold(imapString(f), `\Seen`) {
				msg["read"] = true
			}
		}
		if unreadOnly && msg["read"] == true {
			continue
		}
		if env, ok := attrs["ENVELOPE"].([]interface{}); ok {
			envelopeFields(env, msg)
		}
		// The body comes back as BODY[TEXT]<0> for the partial fetch.
		for name, v := range attrs {
			if strings.HasPrefix(name, "BODY[TEXT]") {
				bodyText := imapString(v)
				if len(bodyText) > 2048 {
					bodyText = bodyText[:2048] + "…"
				}
				msg["body"] = strings.TrimSpace(bodyText)
			}
		}
		results = append(results, msg)
	}

	// Reverse so newest is first
	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}

	// LOGOUT
	_ = send("A4", "LOGOUT")

	return results, nil
}
