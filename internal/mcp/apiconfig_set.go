package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/openaiapi"
)

// maxSettingLen bounds a value before anything looks at it, far above what any setting takes: a
// host name of 253 characters, a colon and a port make an address of at most 259; a path or a list
// of runtimes is well under 4096. A longer value is refused with a text that names only the setting.
func maxSettingLen(key string) int {
	if key == apiconfig.KeyV1Addr {
		return 260
	}
	return 4096
}

// settingHelp is what a model needs to know of each setting to give it a value. The keys of the
// schema come from apiconfig.Specs, so that they cannot drift from the settings there are.
var settingHelp = map[string]string{
	apiconfig.KeyV1Addr:             "Dedicated listener for /v1: host:port with a numeric port, such as 0.0.0.0:9443 (an empty host is every interface); beyond this machine it is TLS only",
	apiconfig.KeyTLSCertFile:        "PEM certificate file of that listener, a path (never contents); only together with tls_key_file; the pair in the daemon's environment wins over it",
	apiconfig.KeyTLSKeyFile:         "PEM key file of that listener, a path; only together with tls_cert_file",
	apiconfig.KeyConfinement:        "Strongest runtime class the server serves: chat-only, sandboxed or any (default: any on loopback, chat-only beyond it)",
	apiconfig.KeyContextConfinement: "Strongest class a key created with context may use: chat-only, sandboxed or any (default chat-only; never above confinement)",
	apiconfig.KeyAutoConfinement:    "Strongest class the auto model may pick: chat-only, sandboxed or any (default chat-only; never above confinement)",
	apiconfig.KeyMaxConcurrent:      "Turns that may run at once, a whole number from 1 to 64 (default 4)",
	apiconfig.KeyTurnTimeout:        "Wall-clock cap of one turn, a Go duration of at least 10s, such as 15m (default 10m)",
	apiconfig.KeyImageRuntimes:      "Runtimes whose models make images, comma-separated, or none to switch image generation off (default codex,antigravity)",
	apiconfig.KeyToolRuntimes:       "Runtimes that serve tool calling, comma-separated, or none to switch it off (default claude,codex)",
}

func apiConfigSetSchema() map[string]interface{} {
	props := map[string]interface{}{}
	for _, sp := range apiconfig.Specs() {
		help := settingHelp[sp.Key]
		if help == "" {
			help = "The " + sp.Env + " setting"
		}
		p := map[string]interface{}{"type": "string", "description": help + " (the syntax of " + sp.Env + ")"}
		if sp.Key == apiconfig.KeyMaxConcurrent {
			delete(p, "type")
			p["anyOf"] = []interface{}{map[string]interface{}{"type": "string"}, map[string]interface{}{"type": "integer"}}
		}
		props[sp.Key] = p
	}
	return objSchema(map[string]interface{}{
		"set": map[string]interface{}{
			"type":                 "object",
			"description":          "Settings to save, each a key with its value as text (max_concurrent may be a number). Only the settings given change.",
			"properties":           props,
			"additionalProperties": false,
		},
		"unset": map[string]interface{}{
			"description": "Settings whose saved value is removed, so that the environment or the default applies: a list of keys (the spelling of a flag, v1-addr, also works), or the single word all for every setting, which also removes saved settings that cannot be read (that needs the operator's --allow-api-exposure).",
			"anyOf": []interface{}{
				map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				map[string]interface{}{"type": "string", "enum": []string{"all"}},
			},
		},
	})
}

func apiConfigSetTool() tool {
	return tool{
		name: "api_config_set",
		description: "Save settings of the OpenAI-compatible API's server, or remove saved ones, in the database: the document of `monoagentcli api config set|unset --json`, which is api_config_get's document for the state after the change " +
			"plus applied, changed (the keys whose saved value is different now) and widening ([{key, reason}]). " +
			"Give set, an object of setting keys to values in the syntax of the setting's environment variable (the keys and what each accepts are in the schema), and/or unset, a list of keys or the single word all. " +
			"Only what is given changes. The checks are the command's, the same code: a value that fails its rule is refused naming the setting and the rule, an empty value is refused (use unset), " +
			"tls_cert_file and tls_key_file must end up together, and nothing is saved from a call that is refused in any part. A value that holds an API key (sk-ma-) is refused: nothing here takes one, and api_config_get shows what is saved. " +
			"A saved setting takes effect when the server starts, never while it runs: this tool restarts nothing (api_config_apply does, which interrupts what the daemon is running), " +
			"and a flag or variable the daemon was given overrides a saved value (api_config_get says which). " +
			"A change that makes the server reach further than it did is refused, and nothing is saved, unless the operator started this MCP server with --allow-api-exposure: " +
			"a dedicated listener beyond this machine, or moved to another host beyond it or to every interface (an empty host, 0.0.0.0 or [::]), " +
			"a higher confinement class (of a listener, of a key created with context or of the auto model), " +
			"a runtime outside the default list, tool calling or image generation switched on again, which includes removing a confinement of chat-only or an image_runtimes of none, " +
			"and removing saved settings that cannot be read (unset all), since what they limited cannot be told. " +
			"No argument can allow it, because the model sets the arguments and only the operator sets that flag. When it refuses it says which setting and why, without any address (api_config_get shows what is saved), " +
			"and that the user can make the change with `monoagentcli api config set ... --yes` (or unset) or in the desktop app. " +
			"When the operator allowed it, the reasons are in widening. Narrowing, max_concurrent, turn_timeout and the TLS files never need it. " +
			"While the saved settings are damaged (a row that cannot be read) every other change fails with a message that starts `the saved settings are damaged`: tell the user, " +
			"who can run `monoagentcli api config unset --all --yes`, or allow this call to remove the row (unset all, with --allow-api-exposure), and the result then says removed_unreadable_row; " +
			"a row that a newer version saved fails too, and nothing here removes it. Two calls at once both land.",
		schema:      apiConfigSetSchema(),
		annotations: map[string]bool{"readOnlyHint": false},
		mutating:    true,
		handler:     toolAPIConfigSet,
	}
}

func toolAPIConfigSet(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	var a struct {
		Set   map[string]json.RawMessage `json:"set"`
		Unset json.RawMessage            `json:"unset"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	ch, err := changeFrom(a.Set, a.Unset)
	if err != nil {
		return nil, err
	}
	// Whether a change that reaches further is allowed is the operator's, decided when this server
	// started. It is read from nowhere in the call.
	ch.Confirm = s.opts.AllowAPIExposure
	rt, err := s.runtime()
	if err != nil {
		return nil, err
	}
	res, err := apiconfig.Apply(ctx, rt.db.DB, s.apiEnv(), ch)
	if err != nil {
		return nil, apiConfigSetError(err, ch)
	}
	return res, nil
}

// changeFrom is the change a call asks for. A value is looked at only after its length and for a
// key, and an error names the setting by its key in the table and never what was sent: a caller may
// have put anything, an API key included, into any of its arguments.
func changeFrom(set map[string]json.RawMessage, unset json.RawMessage) (apiconfig.Change, error) {
	var ch apiconfig.Change
	var problems []string
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if ch.Set == nil {
			ch.Set = make(map[string]string, len(set))
		}
		key, known := apiconfig.LookupKey(name)
		if !known {
			ch.Set[name] = "" // Apply says that it is not a setting, in a text that does not repeat it
			continue
		}
		text, problem := settingText(key, set[name])
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		ch.Set[name] = text
	}
	if len(problems) > 0 {
		return ch, errors.New(strings.Join(problems, "; "))
	}
	keys, all, err := unsetList(unset)
	if err != nil {
		return ch, err
	}
	ch.Unset, ch.All = keys, all
	return ch, nil
}

// settingText is the text of a value given for a setting: a string, or a number (which is the text
// the command would have been given), checked for its length and for an API key. A null is an empty
// value, which Apply refuses as the command does.
func settingText(key string, raw json.RawMessage) (text, problem string) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0 || string(raw) == "null":
	case raw[0] == '"':
		if json.Unmarshal(raw, &text) != nil {
			return "", key + " must be text"
		}
	case raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9'):
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return "", key + " must be text"
		}
		text = n.String()
	default:
		return "", key + " must be text"
	}
	if len(text) > maxSettingLen(key) {
		return "", key + " is too long"
	}
	// What is saved is shown to whoever reads api_config_get, and what is refused is not repeated.
	if strings.Contains(strings.ToLower(text), apikeys.KeyPrefix) {
		return "", key + " must not hold an API key"
	}
	return text, ""
}

var (
	errUnsetShape     = errors.New("unset must be a list of settings (text), or the word all")
	errUnsetAllInList = errors.New("to remove every setting give unset as the word all, not as a list")
)

// unsetList reads unset: a list of keys, or the word all.
func unsetList(raw json.RawMessage) (keys []string, all bool, err error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	switch raw[0] {
	case '"':
		var word string
		if json.Unmarshal(raw, &word) != nil || word != "all" {
			return nil, false, errUnsetShape
		}
		return nil, true, nil
	case '[':
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) != nil {
			return nil, false, errUnsetShape
		}
		for _, el := range list {
			var name string
			if json.Unmarshal(el, &name) != nil {
				return nil, false, errUnsetShape
			}
			if name == "all" {
				return nil, false, errUnsetAllInList
			}
			keys = append(keys, name)
		}
		return keys, false, nil
	}
	return nil, false, errUnsetShape
}

// errBadAddr is the command's rule for v1_addr. The command adds what the parser said of the address,
// and that quotes it.
const errBadAddr = "v1_addr must be host:port, such as 127.0.0.1:9443 or :9443"

// apiConfigSetError is what a model is told of an error of Apply: a change that fails its rules in
// the command's words, and a change that reaches further as a refusal that names the operator's
// switch. Neither repeats an argument.
func apiConfigSetError(err error, ch apiconfig.Change) error {
	var widening *apiconfig.WideningError
	if errors.As(err, &widening) {
		return refusalOf(widening.Widening, ch)
	}
	var invalid *apiconfig.ValidationError
	if errors.As(err, &invalid) {
		msgs := make([]string, len(invalid.Problems))
		for i, p := range invalid.Problems {
			msgs[i] = p.Message
			if p.Key == apiconfig.KeyV1Addr && strings.HasPrefix(p.Message, "v1_addr must be host:port") {
				msgs[i] = errBadAddr
			}
		}
		return errors.New(strings.Join(msgs, "; "))
	}
	return err
}

// refusalOf is the refusal of a change that reaches further when the operator did not allow it: which
// setting and why (the reasons of apiconfig.Widens, without the values of the call), who decides, and
// what the user can do.
func refusalOf(ws []apiconfig.Widening, ch apiconfig.Change) error {
	var b strings.Builder
	b.WriteString("This change makes the server reach further than it did, and this MCP server was not started with --allow-api-exposure, so nothing was saved:")
	repair := false // removing a row that cannot be read: the CLI is what the user has for it
	for _, w := range ws {
		fmt.Fprintf(&b, "\n- %s: %s", w.Key, scrubReason(w.Reason, ch))
		repair = repair || w.Key == apiconfig.WideningKeySavedSettings
	}
	var cmds []string
	if len(ch.Set) > 0 {
		cmds = append(cmds, "`monoagentcli api config set ... --yes`")
	}
	if len(ch.Unset) > 0 {
		cmds = append(cmds, "`monoagentcli api config unset ... --yes`")
	}
	if ch.All {
		cmds = append(cmds, "`monoagentcli api config unset --all --yes`")
	}
	b.WriteString("\nNo argument of this tool can allow it: only the operator can, by starting `monoagentcli mcp` with --allow-api-exposure (or MONOAGENT_MCP_ALLOW_API_EXPOSURE=1). ")
	b.WriteString("The user can make the change themselves with " + strings.Join(cmds, " and "))
	if repair {
		b.WriteString(".")
	} else {
		b.WriteString(", or in the desktop app (Settings › OpenAI-compatible API).")
	}
	return errors.New(b.String())
}

// scrubReason is a reason of the gate without what the call asked for in it, and without any address:
// the reasons name the addresses of a listener (the new one and, for a move, the one saved before) and
// the runtimes outside the default list, which is what makes them useful to a person, and an echo of
// the arguments in an error.
func scrubReason(reason string, ch apiconfig.Change) string {
	var addrs []string
	for name, text := range ch.Set {
		switch key, _ := apiconfig.LookupKey(name); key {
		case apiconfig.KeyV1Addr:
			addrs = append(addrs, text)
		case apiconfig.KeyImageRuntimes:
			reason = scrubRuntimes(reason, text, openaiapi.ParseImageRuntimes)
		case apiconfig.KeyToolRuntimes:
			reason = scrubRuntimes(reason, text, openaiapi.ParseToolRuntimes)
		}
	}
	return scrubAddresses(reason, addrs)
}

// addressPlaceholder is what a reason says where it named an address, or a host.
const addressPlaceholder = "<the address>"

// reasonWord is a word of a reason as far as an address goes: an address, and a host in any spelling, is
// never broken by a space, a parenthesis, a comma or a semicolon.
var reasonWord = regexp.MustCompile(`[^\s(),;]+`)

// scrubAddresses takes out of a reason every address and every host in it. What apiconfig.Widens prints
// is the address as it was saved, the old one next to the new one when a listener moves, and a reason
// that names a host alone (in another spelling of it, with or without brackets or a zone) must not bring
// it back, so the scrub is of what the reason holds: an address wherever a word is one, and a host
// wherever a word is the host of an address of the call or of the reason, whatever its spelling. The
// port alone is harmless and stays. A full stop after a word stays, since it may end the sentence.
func scrubAddresses(reason string, submitted []string) string {
	hosts := map[string]bool{}
	address := func(word string) bool {
		if apiconfig.ValidListenAddr(word) != nil {
			return false
		}
		host, _, _ := net.SplitHostPort(word)
		if key := hostKey(host); key != "" {
			hosts[key] = true
		}
		return true
	}
	for _, text := range submitted {
		// The call may hold what splits a sentence into words (the checker takes any host), so what it
		// named is taken out as it was written too.
		reason = replaceStandalone(reason, text, addressPlaceholder)
		address(text)
	}
	for _, word := range reasonWord.FindAllString(reason, -1) {
		address(strings.TrimRight(word, "."))
	}
	return reasonWord.ReplaceAllStringFunc(reason, func(word string) string {
		bare := strings.TrimRight(word, ".")
		if apiconfig.ValidListenAddr(bare) == nil || hosts[hostKey(bare)] {
			return addressPlaceholder + word[len(bare):]
		}
		return word
	})
}

// hostKey is a host in the one spelling that all of its spellings share, "" for none: in lower case,
// without brackets, a zone or the dot of a full name, and an IP address in its canonical form (which is
// also the IPv4 address that an IPv4-mapped IPv6 one is).
func hostKey(host string) string {
	h := strings.ToLower(host)
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if i := strings.IndexByte(h, '%'); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	if ip := net.ParseIP(h); ip != nil {
		return ip.String()
	}
	return h
}

// replaceStandalone replaces the occurrences of text that stand between the separators of a sentence
// (the start or end of it, a space, a parenthesis, a comma, a semicolon, and a full stop after it), so
// that :9443 is not taken out of 192.168.1.10:9443.
func replaceStandalone(s, text, with string) string {
	if text == "" {
		return s
	}
	var out strings.Builder
	copied := 0
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], text)
		if i < 0 {
			break
		}
		i += from
		end := i + len(text)
		if (i == 0 || strings.IndexByte(" \t\r\n(),;", s[i-1]) >= 0) && (end == len(s) || strings.IndexByte(" \t\r\n(),;.", s[end]) >= 0) {
			out.WriteString(s[copied:i])
			out.WriteString(with)
			copied = end
		}
		from = end
	}
	out.WriteString(s[copied:])
	return out.String()
}

// scrubRuntimes replaces the runtimes of a list that are not in the default list, wherever the
// reason names them as a whole word: the ids are the ones the parser made of the list (lower case,
// as the reason prints them). The default list is public, and a reason says it.
func scrubRuntimes(reason, text string, parse func(string) ([]string, error)) string {
	ids, err := parse(text)
	if err != nil {
		return reason
	}
	defaults, _ := parse("")
	for _, id := range ids {
		if slices.Contains(defaults, id) {
			continue
		}
		word := regexp.MustCompile(`(^|[^a-z0-9-])` + regexp.QuoteMeta(id) + `([^a-z0-9-]|$)`)
		reason = word.ReplaceAllString(reason, "${1}<runtime>${2}")
	}
	return reason
}
