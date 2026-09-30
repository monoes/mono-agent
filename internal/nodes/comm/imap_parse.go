package comm

import (
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/htmlindex"
)

// maxIMAPLiteral caps one {n} literal. The node asks for at most 2 KB of
// body; anything far larger is a broken or hostile server.
const maxIMAPLiteral = 1 << 20

// imapResponse is one server response: its text with each {n} marker
// kept, and the literals those markers announced, in order.
type imapResponse struct {
	text     string
	literals []string
}

// readIMAPResponse reads one response line and every literal it
// announces: a line ending in {n} is followed by n raw bytes, then the
// rest of the response on the next line.
func readIMAPResponse(readLine func() (string, error), r io.Reader) (imapResponse, error) {
	var resp imapResponse
	for {
		line, err := readLine()
		if err != nil {
			return resp, err
		}
		resp.text += line
		n, ok := literalSuffix(line)
		if !ok {
			return resp, nil
		}
		if n > maxIMAPLiteral {
			return resp, fmt.Errorf("IMAP literal of %d bytes is over the %d byte limit", n, maxIMAPLiteral)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return resp, err
		}
		resp.literals = append(resp.literals, string(buf))
	}
}

// literalSuffix reports the n of a trailing {n} (or {n+}).
func literalSuffix(line string) (int, bool) {
	if !strings.HasSuffix(line, "}") {
		return 0, false
	}
	open := strings.LastIndexByte(line, '{')
	if open < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(line[open+1:len(line)-1], "+"))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// imapNIL is the NIL atom, kept apart from the string "NIL".
type imapNIL struct{}

// imapParser reads an IMAP s-expression: atoms, NIL, quoted strings with
// \" and \\ escapes, {n} literals and parenthesized lists. Values are
// string, imapNIL or []interface{}.
type imapParser struct {
	s        string
	pos      int
	literals []string
}

func (p *imapParser) skipSpace() {
	for p.pos < len(p.s) && p.s[p.pos] == ' ' {
		p.pos++
	}
}

func (p *imapParser) value() (interface{}, error) {
	p.skipSpace()
	if p.pos >= len(p.s) {
		return nil, io.ErrUnexpectedEOF
	}
	switch p.s[p.pos] {
	case '(':
		p.pos++
		var list []interface{}
		for {
			p.skipSpace()
			if p.pos >= len(p.s) {
				return nil, io.ErrUnexpectedEOF
			}
			if p.s[p.pos] == ')' {
				p.pos++
				return list, nil
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
	case ')':
		return nil, fmt.Errorf("unexpected ')' at %d", p.pos)
	case '"':
		var b strings.Builder
		for p.pos++; p.pos < len(p.s); p.pos++ {
			c := p.s[p.pos]
			if c == '\\' && p.pos+1 < len(p.s) {
				p.pos++
				b.WriteByte(p.s[p.pos])
				continue
			}
			if c == '"' {
				p.pos++
				return b.String(), nil
			}
			b.WriteByte(c)
		}
		return nil, io.ErrUnexpectedEOF
	case '{':
		end := strings.IndexByte(p.s[p.pos:], '}')
		if end < 0 {
			return nil, io.ErrUnexpectedEOF
		}
		p.pos += end + 1
		if len(p.literals) == 0 {
			return nil, fmt.Errorf("literal marker without a literal")
		}
		lit := p.literals[0]
		p.literals = p.literals[1:]
		return lit, nil
	}
	// Atom: up to a space or paren. Keeps section specs like BODY[TEXT]<0>
	// (whose brackets never hold a space in what this node fetches).
	start := p.pos
	for p.pos < len(p.s) && !strings.ContainsRune(" ()\"{", rune(p.s[p.pos])) {
		p.pos++
	}
	atom := p.s[start:p.pos]
	if strings.EqualFold(atom, "NIL") {
		return imapNIL{}, nil
	}
	return atom, nil
}

// parseFetchResponse parses "* <seq> FETCH (<name> <value> ...)" into its
// attributes, keyed by upper-cased name. ok is false for any other response.
func parseFetchResponse(resp imapResponse) (map[string]interface{}, bool) {
	p := &imapParser{s: resp.text, literals: resp.literals}
	var head []string
	for len(head) < 3 {
		v, err := p.value()
		atom, isAtom := v.(string)
		if err != nil || !isAtom {
			return nil, false
		}
		head = append(head, atom)
	}
	if head[0] != "*" || !strings.EqualFold(head[2], "FETCH") {
		return nil, false
	}
	if _, err := strconv.Atoi(head[1]); err != nil {
		return nil, false
	}
	v, err := p.value()
	list, isList := v.([]interface{})
	if err != nil || !isList {
		return nil, false
	}
	attrs := map[string]interface{}{}
	for i := 0; i+1 < len(list); i += 2 {
		if name, ok := list[i].(string); ok {
			attrs[strings.ToUpper(name)] = list[i+1]
		}
	}
	return attrs, true
}

// imapString is an nstring's text; NIL and non-strings are "".
func imapString(v interface{}) string {
	s, _ := v.(string)
	return s
}

// envelopeFields maps an ENVELOPE (date subject from sender reply-to to cc
// bcc in-reply-to message-id) onto the node's item fields.
func envelopeFields(env []interface{}, into map[string]interface{}) {
	if len(env) < 10 {
		return
	}
	into["date"] = imapString(env[0])
	into["subject"] = decodeMIMEHeader(imapString(env[1]))
	into["from"] = firstIMAPAddress(env[2])
	into["message_id"] = imapString(env[9])
}

// firstIMAPAddress renders the first (name adl mailbox host) of an address
// list as "Name <mailbox@host>", or just the address without a name.
func firstIMAPAddress(v interface{}) string {
	list, _ := v.([]interface{})
	if len(list) == 0 {
		return ""
	}
	addr, _ := list[0].([]interface{})
	if len(addr) < 4 {
		return ""
	}
	name := decodeMIMEHeader(imapString(addr[0]))
	mailbox, host := imapString(addr[2]), imapString(addr[3])
	email := ""
	if mailbox != "" && host != "" {
		email = mailbox + "@" + host
	}
	if name != "" && email != "" {
		return name + " <" + email + ">"
	}
	return email
}

var mimeDecoder = &mime.WordDecoder{
	CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
		enc, err := htmlindex.Get(charset)
		if err != nil {
			return nil, err
		}
		return enc.NewDecoder().Reader(input), nil
	},
}

// decodeMIMEHeader decodes RFC 2047 encoded-words (=?charset?B|Q?...?=);
// text it can't decode stays as it is.
func decodeMIMEHeader(s string) string {
	out, err := mimeDecoder.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}
