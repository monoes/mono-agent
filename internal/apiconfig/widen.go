package apiconfig

import (
	"fmt"
	"slices"
	"strings"

	"github.com/monoes/mono-agent/internal/openaiapi"
	"github.com/monoes/mono-agent/internal/tlsserve"
)

// Widening is one way a change makes the server reach further than it did: a stable Key, and
// a Reason in one sentence, to be shown as it is.
type Widening struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// Widens says how the change from before to after makes the server reach further. It
// compares the effective policy the two saved documents give, not the words in them, so
// that an unset which takes a value back to a higher default counts, and saving a value
// that spells the default does not:
//
//   - v1_addr: the dedicated listener binds beyond this machine where it did not (none, or
//     loopback);
//   - confinement, context_confinement, auto_confinement: the class rises, on the loopback
//     kind of listener or on the network kind. Both kinds are judged in every document, named
//     dedicated listener or not, because the daemon's own environment may supply the address
//     of one; context and auto are judged after the cap that confinement puts on them;
//   - image_runtimes, tool_runtimes: the list gains a runtime that is not in the built-in
//     default list (and was not in the old list), or leaves none.
//
// max_concurrent, turn_timeout and the TLS files are not exposure. The result is in the order
// of the settings. A document that does not pass Validate is read with its invalid values
// taken for unset; refusing it is Validate's business.
func Widens(before, after Settings) []Widening {
	var out []Widening
	if w, ok := addressWidening(before, after); ok {
		out = append(out, w)
	}
	for _, d := range classDimensions {
		for _, k := range listenerKinds {
			b, a := classesOf(before, k.network), classesOf(after, k.network)
			if from, to := d.pick(b), d.pick(a); to > from {
				out = append(out, Widening{Key: d.key + "." + k.name, Reason: d.reason(k, className(to), className(from))})
			}
		}
	}
	for _, r := range []struct {
		key, what string
		parse     func(string) ([]string, error)
		before    string
		after     string
	}{
		{KeyImageRuntimes, "Image generation", openaiapi.ParseImageRuntimes, before.ImageRuntimes, after.ImageRuntimes},
		{KeyToolRuntimes, "Tool calling", openaiapi.ParseToolRuntimes, before.ToolRuntimes, after.ToolRuntimes},
	} {
		if reason, ok := runtimesWidening(r.what, r.parse, r.before, r.after); ok {
			out = append(out, Widening{Key: r.key, Reason: reason})
		}
	}
	return out
}

// addressWidening is rule (a): the new address is not loopback, and there was none or a
// loopback one. (A move between two addresses beyond the machine is not seen: P9 of the plan.)
func addressWidening(before, after Settings) (Widening, bool) {
	b, a := listenAddr(before), listenAddr(after)
	if a == "" || tlsserve.IsLoopbackAddr(a) {
		return Widening{}, false
	}
	if b != "" && !tlsserve.IsLoopbackAddr(b) {
		return Widening{}, false
	}
	class := className(classesOf(after, true).confinement)
	return Widening{
		Key: KeyV1Addr,
		Reason: fmt.Sprintf("The dedicated /v1 listener would listen on %s, beyond this machine, and serve runtimes up to %s; it did not listen beyond this machine before.",
			a, class),
	}, true
}

// listenAddr is the dedicated listener's address, "" when there is none or the text is not
// an address.
func listenAddr(s Settings) string {
	if ValidListenAddr(s.V1Addr) != nil {
		return ""
	}
	return s.V1Addr
}

// A kind of listener: the main one is always on loopback, and the dedicated one is a network
// listener unless it is bound there.
type listenerKind struct {
	name    string
	network bool
}

var listenerKinds = []listenerKind{{"loopback", false}, {"network", true}}

func (k listenerKind) where() string {
	if k.network {
		return "a /v1 listener beyond this machine"
	}
	return "the /v1 listener on this machine"
}

// classes are the strongest runtime class each use may reach on one kind of listener.
type classes struct {
	confinement, context, auto openaiapi.Class
}

// classesOf is what the server serves on a listener of one kind: confinement unset is any on
// loopback and chat-only beyond it, context and auto unset are chat-only, and neither is ever
// above confinement. Text that is not a class is not saved.
func classesOf(s Settings, network bool) classes {
	conf := openaiapi.Unconfined
	if network {
		conf = openaiapi.ChatOnly
	}
	if c, ok := classOf(s.Confinement); ok {
		conf = c
	}
	limit := func(text string) openaiapi.Class {
		c, ok := classOf(text)
		if !ok {
			c = openaiapi.ChatOnly
		}
		return min(c, conf)
	}
	return classes{confinement: conf, context: limit(s.ContextConfinement), auto: limit(s.AutoConfinement)}
}

func classOf(text string) (openaiapi.Class, bool) {
	p, err := openaiapi.ParsePolicy(text)
	if err != nil {
		return 0, false
	}
	return p.Max, true
}

func className(c openaiapi.Class) string { return openaiapi.Policy{Max: c}.String() }

// classDimensions are the three classes, in the order of the settings.
var classDimensions = []struct {
	key    string
	pick   func(classes) openaiapi.Class
	reason func(k listenerKind, to, from string) string
}{
	{KeyConfinement, func(c classes) openaiapi.Class { return c.confinement }, func(k listenerKind, to, from string) string {
		if k.network {
			return fmt.Sprintf("%s (a dedicated listener from v1_addr, --v1-addr or MONOAGENT_API_V1_ADDR) would serve runtimes up to %s, where it served up to %s.",
				capitalise(k.where()), to, from)
		}
		return fmt.Sprintf("%s would serve runtimes up to %s, where it served up to %s.", capitalise(k.where()), to, from)
	}},
	{KeyContextConfinement, func(c classes) openaiapi.Class { return c.context }, func(k listenerKind, to, from string) string {
		return fmt.Sprintf("A key created with --context could use runtimes up to %s on %s, where it could use up to %s.", to, k.where(), from)
	}},
	{KeyAutoConfinement, func(c classes) openaiapi.Class { return c.auto }, func(k listenerKind, to, from string) string {
		return fmt.Sprintf("The auto model could pick runtimes up to %s on %s, where it could pick up to %s.", to, k.where(), from)
	}},
}

func capitalise(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// runtimesWidening is rule (c) for one list: a list that leaves none, or gains a runtime that
// is neither in the built-in default list nor in the old list. An unset list is the default
// list, none is the empty one, and text that is not a list is the default.
func runtimesWidening(what string, parse func(string) ([]string, error), before, after string) (string, bool) {
	defaults, _ := parse("")
	effective := func(text string) []string {
		list, err := parse(text)
		if err != nil {
			return defaults
		}
		return list
	}
	b, a := effective(before), effective(after)
	if len(b) == 0 && len(a) > 0 {
		return fmt.Sprintf("%s, which is switched off, would be served by %s.", what, strings.Join(a, ", ")), true
	}
	var gained []string
	for _, r := range a {
		if !slices.Contains(b, r) && !slices.Contains(defaults, r) {
			gained = append(gained, r)
		}
	}
	if len(gained) == 0 {
		return "", false
	}
	return fmt.Sprintf("%s would be served by %s, beyond the default list (%s).", what, strings.Join(gained, ", "), strings.Join(defaults, ", ")), true
}
