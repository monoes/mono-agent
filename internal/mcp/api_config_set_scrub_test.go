package mcp

// A reason of the exposure gate is shown to a model without an address or a host: the address of the
// call, the address that was saved before it, and each of their hosts in any spelling. The reasons that
// apiconfig.Widens gives name the addresses in full today, and a future reason may name a host alone, so
// the scrub is of what a reason holds, not of the one shape it has now. These tests give it the shapes
// by hand; the refusals of real changes are in api_config_set_echo_test.go.

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
)

func addrChange(addr string) apiconfig.Change {
	return apiconfig.Change{Set: map[string]string{"v1_addr": addr}}
}

const here = "<the address>"

func TestScrubReasonTakesOutEveryFormOfTheAddressOfTheCall(t *testing.T) {
	for _, c := range []struct {
		name  string
		addr  string   // the address of the call
		forms []string // what a reason may print of it
	}{
		{"an IPv4 address", "10.0.0.5:9443", []string{"10.0.0.5:9443", "10.0.0.5", "[::ffff:10.0.0.5]", "::ffff:10.0.0.5", "[::ffff:10.0.0.5]:9443"}},
		{"a host name", "Host.Example:9443", []string{"Host.Example:9443", "host.example:9443", "HOST.EXAMPLE", "host.example", "host.example.:9443"}},
		{"a full host name", "host.example.:9443", []string{"host.example.:9443", "host.example:9443", "host.example", "HOST.EXAMPLE"}},
		{"every interface", ":9443", []string{":9443"}},
		{"an unspecified IPv4 address", "0.0.0.0:9443", []string{"0.0.0.0:9443", "0.0.0.0"}},
		{"an unspecified IPv6 address", "[::]:9443", []string{"[::]:9443", "[::]", "::", "[0:0:0:0:0:0:0:0]", "0:0:0:0:0:0:0:0"}},
		{"an IPv6 address with a zone", "[fe80::1%eth0]:9443", []string{
			"[fe80::1%eth0]:9443", "[fe80::1%eth0]", "fe80::1%eth0", "[fe80::1]:9443", "[fe80::1]", "fe80::1",
			"[FE80::1%ETH0]:9443", "FE80::1", "fe80:0:0:0:0:0:0:1", "[fe80:0:0:0:0:0:0:1]", "fe80::1%eth9",
		}},
		{"an IPv6 address without one", "[2001:db8::1]:9443", []string{"[2001:db8::1]:9443", "[2001:db8::1]", "2001:db8::1", "2001:DB8::1", "2001:db8:0:0:0:0:0:1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, form := range c.forms {
				// In the middle of a sentence, in a list, next to a parenthesis, and at the end of one.
				for _, around := range []struct{ reason, want string }{
					{"It would listen on %s, beyond this machine.", "It would listen on " + here + ", beyond this machine."},
					{"It would listen on every interface (%s), where it did not.", "It would listen on every interface (" + here + "), where it did not."},
					{"It would move to %s.", "It would move to " + here + "."},
					{"It would move to %s; the rest stays.", "It would move to " + here + "; the rest stays."},
				} {
					reason := strings.Replace(around.reason, "%s", form, 1)
					if got := scrubReason(reason, addrChange(c.addr), ""); got != around.want {
						t.Errorf("%q\n got %q\nwant %q", reason, got, around.want)
					}
				}
			}
		})
	}
}

// Whatever address a reason prints is scrubbed, the one saved before the call too: the call names only
// the new one, and the old one is in the reason of a move.
func TestScrubReasonTakesOutTheAddressesTheCallDoesNotName(t *testing.T) {
	for _, c := range []struct{ name, reason, want string }{
		{
			"the old and the new address of a move",
			"The dedicated /v1 listener would move from 192.168.1.10:9443 to 10.0.0.5:9443, another address beyond this machine, and serve runtimes up to chat-only.",
			"The dedicated /v1 listener would move from " + here + " to " + here + ", another address beyond this machine, and serve runtimes up to chat-only.",
		},
		{
			"an empty host after a name that ends in the same port",
			"The dedicated /v1 listener would listen on every interface (:9443), where it listened only on 192.168.1.10:9443 before, and serve runtimes up to chat-only.",
			"The dedicated /v1 listener would listen on every interface (" + here + "), where it listened only on " + here + " before, and serve runtimes up to chat-only.",
		},
		{
			"the host of an address, printed alone later",
			"It would move from [fe80::1%eth0]:9443 to 10.0.0.5:9443 (fe80::1 is where it is now).",
			"It would move from " + here + " to " + here + " (" + here + " is where it is now).",
		},
		{
			"an address that ends the sentence",
			"It would move to 10.0.0.5:9443 from 192.168.1.10:9443.",
			"It would move to " + here + " from " + here + ".",
		},
		{
			"a host of the old address in other capitals",
			"It would move from Old.Example:9443 to 10.0.0.5:9443; OLD.EXAMPLE is not served after that.",
			"It would move from " + here + " to " + here + "; " + here + " is not served after that.",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := scrubReason(c.reason, addrChange("10.0.0.5:9443"), ""); got != c.want {
				t.Errorf("\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// An IP address that is not the call's and not saved is a host all the same, and goes too: a reason is
// shown without any host, whatever it is made of.
func TestScrubReasonTakesOutAnIPAddressWhoseHostNobodyNamed(t *testing.T) {
	for _, c := range []struct{ reason, want string }{
		{"It would listen on 172.16.0.9., beyond this machine.", "It would listen on " + here + "., beyond this machine."},
		{"It would listen on 10.0.0.50 and [2001:db8::7] as well.", "It would listen on " + here + " and " + here + " as well."},
		{"It would listen on fe80::9%eth1 and ::ffff:172.16.0.9.", "It would listen on " + here + " and " + here + "."},
		{"Version 2.22.0 of the 10 runtimes stays.", "Version 2.22.0 of the 10 runtimes stays."},
	} {
		if got := scrubReason(c.reason, addrChange("10.0.0.5:9443"), ""); got != c.want {
			t.Errorf("%q\n got %q\nwant %q", c.reason, got, c.want)
		}
	}
}

// The address saved before the call may be printed too, by a reason of a move, and the call did not send
// it: it is given to the scrub as it was saved, so that a host that holds a space or a comma (the checker
// takes any text before the last colon) does not come back in pieces.
func TestScrubReasonTakesOutTheSavedAddressWhoseHostHoldsSeparators(t *testing.T) {
	for _, saved := range []string{"zz secret-host:9443", "a,b;c (d):9443", "x y:1"} {
		reason := "It would move from " + saved + " to 10.0.0.5:9443, another address beyond this machine, and from " + saved + "."
		want := "It would move from " + here + " to " + here + ", another address beyond this machine, and from " + here + "."
		if got := scrubReason(reason, addrChange("10.0.0.5:9443"), saved); got != want {
			t.Errorf("%q\n got %q\nwant %q", saved, got, want)
		}
	}
}

// A full stop after a host may be the one that ends the sentence, so it stays: the host is gone, and
// the sentence keeps its end.
func TestScrubReasonKeepsTheFullStopAfterAHost(t *testing.T) {
	for _, c := range []struct{ reason, want string }{
		{"It would listen on host.example., beyond this machine.", "It would listen on " + here + "., beyond this machine."},
		{"It would listen on host.example.", "It would listen on " + here + "."},
		{"It would listen on 10.0.0.5.", "It would listen on " + here + "."},
	} {
		addr := "host.example:9443"
		if strings.Contains(c.reason, "10.0.0.5") {
			addr = "10.0.0.5:9443"
		}
		if got := scrubReason(c.reason, addrChange(addr), ""); got != c.want {
			t.Errorf("%q\n got %q\nwant %q", c.reason, got, c.want)
		}
	}
}

// What is not an address stays: the port alone, the setting names and the words of the sentence, even
// when a host is named like one of them or holds the same digits.
func TestScrubReasonLeavesWhatIsNotAnAddress(t *testing.T) {
	for _, c := range []struct{ name, addr, reason, want string }{
		{
			"the port alone and the names of the settings",
			"10.0.0.5:9443",
			"A /v1 listener beyond this machine (a dedicated listener from v1_addr, --v1-addr or MONOAGENT_API_V1_ADDR) would serve runtimes up to any, where it served up to chat-only; port 9443.",
			"A /v1 listener beyond this machine (a dedicated listener from v1_addr, --v1-addr or MONOAGENT_API_V1_ADDR) would serve runtimes up to any, where it served up to chat-only; port 9443.",
		},
		{
			"a word that holds the digits of a host",
			"10.0.0.5:9443",
			"It would listen on v10.0.0.5 and on 10.0.0.5x, which are not hosts.",
			"It would listen on v10.0.0.5 and on 10.0.0.5x, which are not hosts.",
		},
		{
			"a word with a colon and no port number",
			"10.0.0.5:9443",
			"It would listen on 10.0.0.5:9443, as port:name says, and the word port stays.",
			"It would listen on " + here + ", as port:name says, and the word port stays.",
		},
		{
			"a host that is a part of a word",
			"in:9443",
			"It would listen on every interface (in:9443), beyond this machine.",
			"It would listen on every interface (" + here + "), beyond this machine.",
		},
		{
			"the saved settings that cannot be read",
			"10.0.0.5:9443",
			"The saved settings cannot be read, so what they limited cannot be told: removing them returns every setting to its default, which may reach further.",
			"The saved settings cannot be read, so what they limited cannot be told: removing them returns every setting to its default, which may reach further.",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := scrubReason(c.reason, addrChange(c.addr), ""); got != c.want {
				t.Errorf("\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// The address of the call goes whatever it holds: a host that is no host (the checker takes any text
// before the last colon) is not a word of the reason, and is still not shown.
func TestScrubReasonTakesOutAnAddressWhoseHostHoldsSeparators(t *testing.T) {
	for _, addr := range []string{"my secret thing:9443", "a,b;c (d):9443", "x y:1"} {
		reason := "It would listen on " + addr + ", beyond this machine, and on " + addr + "."
		want := "It would listen on " + here + ", beyond this machine, and on " + here + "."
		if got := scrubReason(reason, addrChange(addr), ""); got != want {
			t.Errorf("%q\n got %q\nwant %q", addr, got, want)
		}
	}
}
