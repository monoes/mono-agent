package service

import (
	"net/url"
	"strings"
	"testing"
)

// TestOutlookListMessagesURLEscapesODataLiteral guards the OData injection
// fix: a from-address containing a single quote must be escaped by doubling
// the quote (OData v4 rule), never interpolated raw — a raw quote would
// break out of the $filter string literal and let the address suffix be
// parsed as filter syntax.
func TestOutlookListMessagesURLEscapesODataLiteral(t *testing.T) {
	u := outlookListMessagesURL("inbox", 10, map[string]interface{}{
		"from_address": "o'brien@x.com",
	})
	want := "from/emailAddress/address eq 'o''brien@x.com'"
	if !strings.Contains(u, url.QueryEscape(want)) {
		t.Errorf("list URL %q\n\tdoes not contain the OData-escaped filter %q", u, want)
	}
	// The unescaped address must not appear inside a filter that Graph would
	// parse as a broken-out literal.
	broken := "from/emailAddress/address eq 'o'brien@x.com'"
	if strings.Contains(u, url.QueryEscape(broken)) {
		t.Errorf("list URL %q contains the unescaped (broken) filter", u)
	}
}

// TestOutlookListMessagesURLEscapesMailboxSegment guards the mailbox
// path-segment escape: a mailbox value containing '/', '?', or '#' must not
// be able to re-shape the request path.
func TestOutlookListMessagesURLEscapesMailboxSegment(t *testing.T) {
	mailbox := "arc/hive?x#y"
	u := outlookListMessagesURL(mailbox, 10, nil)

	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse %q: %v", u, err)
	}
	if !strings.Contains(parsed.EscapedPath(), url.PathEscape(mailbox)) {
		t.Errorf("path %q does not contain the escaped mailbox %q", parsed.EscapedPath(), url.PathEscape(mailbox))
	}
	if strings.Contains(parsed.EscapedPath(), mailbox) {
		t.Errorf("raw mailbox %q leaked into path %q", mailbox, parsed.EscapedPath())
	}
}

// TestOutlookListMessagesURLFilterAndSearch covers the non-security
// behavior of the extracted builder: unread_only + from_address join with
// " and ", and $search takes precedence over $filter (Graph rejects both
// in one request).
func TestOutlookListMessagesURLFilterAndSearch(t *testing.T) {
	u := outlookListMessagesURL("inbox", 25, map[string]interface{}{
		"unread_only":  true,
		"from_address": "a@b.com",
		"$top_ignored": nil,
	})
	want := "isRead eq false and from/emailAddress/address eq 'a@b.com'"
	if !strings.Contains(u, url.QueryEscape(want)) {
		t.Errorf("URL %q missing combined filter %q", u, want)
	}
	if !strings.Contains(u, "$top=25") {
		t.Errorf("URL %q missing $top=25", u)
	}

	s := outlookListMessagesURL("inbox", 10, map[string]interface{}{
		"search":       "invoice",
		"from_address": "a@b.com",
	})
	if !strings.Contains(s, "$search=") {
		t.Errorf("URL %q missing $search", s)
	}
	if strings.Contains(s, "$filter=") {
		t.Errorf("URL %q must not combine $search and $filter", s)
	}
}

// TestOutlookMessageURLEscapesMessageID guards the Graph path-segment
// escape: a message id containing '?', '#', or '/' must be percent-encoded
// so it cannot truncate or redirect the request path.
func TestOutlookMessageURLEscapesMessageID(t *testing.T) {
	id := "AAMkAGI=?#weird/id"

	for _, action := range []string{"", "send", "attachments", "reply"} {
		got := outlookMessageURL(id, action)
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatalf("action %q: parse %q: %v", action, got, err)
		}
		if !strings.Contains(parsed.EscapedPath(), url.PathEscape(id)) {
			t.Errorf("action %q: path %q does not contain escaped id %q", action, parsed.EscapedPath(), url.PathEscape(id))
		}
		if strings.Contains(parsed.EscapedPath(), id) {
			t.Errorf("action %q: raw id leaked into path %q", action, parsed.EscapedPath())
		}
		if action != "" && !strings.HasSuffix(parsed.EscapedPath(), url.PathEscape(id)+"/"+action) {
			t.Errorf("action %q: path %q lost the action segment", action, parsed.EscapedPath())
		}
	}
}

// TestEscapeODataString pins the OData escape rule on its own.
func TestEscapeODataString(t *testing.T) {
	cases := map[string]string{
		"plain@x.com":  "plain@x.com",
		"o'brien@x.co": "o''brien@x.co",
		"''":           "''''",
	}
	for in, want := range cases {
		if got := escapeODataString(in); got != want {
			t.Errorf("escapeODataString(%q) = %q, want %q", in, got, want)
		}
	}
}
