package httpapi

import "testing"

func TestResolveAddr(t *testing.T) {
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "")
	if got := ResolveAddr(""); got != defaultAddr {
		t.Errorf("no flag, no env: %q, want the loopback default %q", got, defaultAddr)
	}
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "127.0.0.1:9999")
	if got := ResolveAddr(""); got != "127.0.0.1:9999" {
		t.Errorf("env only: %q", got)
	}
	if got := ResolveAddr("127.0.0.1:7777"); got != "127.0.0.1:7777" {
		t.Errorf("an explicit address must beat the environment: %q", got)
	}
}

func TestNewServerAppliesResolveAddr(t *testing.T) {
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "127.0.0.1:9123")
	s, err := NewServer(Options{DBPath: t.TempDir() + "/addr.db", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Addr() != "127.0.0.1:9123" {
		t.Errorf("Addr() = %q, want the environment's address", s.Addr())
	}
}
