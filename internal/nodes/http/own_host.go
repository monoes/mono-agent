package httpnodes

import (
	"net"
	"os"
	"strings"
)

// interfaceAddrs and hostname are this machine's addresses and name
// (variables so tests can stand in a machine).
var (
	interfaceAddrs = net.InterfaceAddrs
	hostname       = os.Hostname
)

// ownHost reports whether host is a loopback address or the host this
// machine's webhook server listens on (MONOAGENT_WEBHOOK_ADDR).
//
// A wildcard bind ("0.0.0.0:…", "[::]:…", or no host, ":…") listens on
// every address the machine has, so then any of them is this machine: an
// interface address, or the machine's name (os.Hostname, its short form,
// or that with ".local"). Without this a request to the machine's LAN IP or
// name got no trace header and started a fresh chain: safe, but the loop
// was not counted. Names are compared, never resolved: a DNS lookup per
// request is slow, and a name that merely resolves here (a load balancer's)
// is not proof the request stays on this machine.
func ownHost(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := parseIP(host)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	addr := strings.TrimSpace(os.Getenv("MONOAGENT_WEBHOOK_ADDR"))
	if addr == "" {
		return false
	}
	bind, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if bindIP := parseIP(bind); bind == "" || (bindIP != nil && bindIP.IsUnspecified()) {
		return thisMachine(host, ip)
	} else if bindIP != nil && ip != nil {
		return bindIP.Equal(ip)
	}
	return strings.EqualFold(bind, host)
}

// thisMachine reports whether host (ip, when it is an address) names this
// machine: one of its interface addresses, the unspecified address (which
// connects to the machine itself), or its name.
func thisMachine(host string, ip net.IP) bool {
	if ip != nil {
		if ip.IsUnspecified() {
			return true
		}
		addrs, err := interfaceAddrs()
		if err != nil {
			return false
		}
		for _, a := range addrs {
			var own net.IP
			switch v := a.(type) {
			case *net.IPNet:
				own = v.IP
			case *net.IPAddr:
				own = v.IP
			}
			if own != nil && own.Equal(ip) {
				return true
			}
		}
		return false
	}
	name, err := hostname()
	if err != nil || name == "" {
		return false
	}
	name = strings.TrimSuffix(name, ".")
	short := name
	if i := strings.IndexByte(short, '.'); i > 0 {
		short = short[:i]
	}
	for _, n := range []string{name, short, short + ".local"} {
		if strings.EqualFold(host, n) {
			return true
		}
	}
	return false
}

// parseIP parses an address, dropping an IPv6 zone ("fe80::1%eth0").
func parseIP(s string) net.IP {
	if i := strings.IndexByte(s, '%'); i >= 0 {
		s = s[:i]
	}
	return net.ParseIP(s)
}
