package httpnodes

import (
	"errors"
	"net"
	"testing"
)

// standIn makes this test's machine have the given addresses and name.
func standIn(t *testing.T, name string, addrs ...string) {
	t.Helper()
	oldAddrs, oldName := interfaceAddrs, hostname
	t.Cleanup(func() { interfaceAddrs, hostname = oldAddrs, oldName })
	interfaceAddrs = func() ([]net.Addr, error) {
		var out []net.Addr
		for _, a := range addrs {
			_, n, err := net.ParseCIDR(a)
			if err != nil {
				t.Fatal(err)
			}
			ip, _, _ := net.ParseCIDR(a)
			out = append(out, &net.IPNet{IP: ip, Mask: n.Mask})
		}
		return out, nil
	}
	hostname = func() (string, error) { return name, nil }
}

// #132 item 4: with a wildcard bind every address of this machine, and its
// name, is where a webhook of this machine can be, so a self-call by LAN
// address or name keeps its trace. Other hosts still do not get it.
func TestOwnHostWithAWildcardBind(t *testing.T) {
	standIn(t, "studio.example.lan", "127.0.0.1/8", "192.168.1.20/24", "fe80::1c2a:3bff:fe4d:5e6f/64", "2001:db8::20/64")
	mine := []string{"192.168.1.20", "fe80::1c2a:3bff:fe4d:5e6f", "fe80::1c2a:3bff:fe4d:5e6f%eth0", "2001:db8::20",
		"studio.example.lan", "STUDIO", "studio.local", "studio.example.lan.", "0.0.0.0", "localhost", "127.0.0.1", "::1"}
	notMine := []string{"192.168.1.21", "2001:db8::21", "api.example.com", "studio2", "example.lan"}
	for _, bind := range []string{"0.0.0.0:9321", "[::]:9321", ":9321"} {
		t.Setenv("MONOAGENT_WEBHOOK_ADDR", bind)
		for _, h := range mine {
			if !ownHost(h) {
				t.Errorf("bind %s: %s is this machine, ownHost said no", bind, h)
			}
		}
		for _, h := range notMine {
			if ownHost(h) {
				t.Errorf("bind %s: %s is not this machine, ownHost said yes", bind, h)
			}
		}
	}
}

// A specific bind still matches only itself (and loopback); without
// MONOAGENT_WEBHOOK_ADDR only loopback is this machine.
func TestOwnHostWithASpecificBind(t *testing.T) {
	standIn(t, "studio", "192.168.1.20/24")
	t.Setenv("MONOAGENT_WEBHOOK_ADDR", "192.168.1.20:9321")
	if !ownHost("192.168.1.20") || !ownHost("127.0.0.1") {
		t.Error("the bound address or loopback was not this machine")
	}
	if ownHost("studio") {
		t.Error("the machine's name matched a bind to one address")
	}
	t.Setenv("MONOAGENT_WEBHOOK_ADDR", "")
	if ownHost("192.168.1.20") || !ownHost("localhost") {
		t.Error("with no bind set, only loopback is this machine")
	}
}

// When the machine's addresses cannot be read, nothing but loopback is
// taken for this machine: the request goes without the token (safe).
func TestOwnHostFailsSafe(t *testing.T) {
	standIn(t, "")
	interfaceAddrs = func() ([]net.Addr, error) { return nil, errors.New("no netlink") }
	t.Setenv("MONOAGENT_WEBHOOK_ADDR", "0.0.0.0:9321")
	if ownHost("192.168.1.20") || ownHost("") {
		t.Error("an unreadable machine matched")
	}
}
