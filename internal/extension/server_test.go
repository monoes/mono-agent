package extension

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestResolveListenAddr_EnvOverride(t *testing.T) {
	t.Setenv(ExtensionPortEnv, "9500")
	got, err := resolveListenAddr("127.0.0.1:9222")
	if err != nil {
		t.Fatalf("resolveListenAddr: %v", err)
	}
	if got != "127.0.0.1:9500" {
		t.Errorf("resolveListenAddr = %q, want 127.0.0.1:9500", got)
	}
}

func TestResolveListenAddr_EnvOverrideInvalid(t *testing.T) {
	for _, val := range []string{"abc", "0", "-1", "70000", "9222 extra"} {
		t.Setenv(ExtensionPortEnv, val)
		if _, err := resolveListenAddr("127.0.0.1:9222"); err == nil {
			t.Errorf("resolveListenAddr with %s=%q should fail, got nil", ExtensionPortEnv, val)
		}
	}
}

func TestResolveListenAddr_NoEnv(t *testing.T) {
	t.Setenv(ExtensionPortEnv, "")
	got, err := resolveListenAddr("127.0.0.1:9222")
	if err != nil {
		t.Fatalf("resolveListenAddr: %v", err)
	}
	if got != "127.0.0.1:9222" {
		t.Errorf("resolveListenAddr = %q, want unchanged address", got)
	}
}

func TestListenCandidates_DefaultPortHasFallback(t *testing.T) {
	t.Setenv(ExtensionPortEnv, "")
	cands, err := listenCandidates("127.0.0.1:9222")
	if err != nil {
		t.Fatalf("listenCandidates: %v", err)
	}
	if len(cands) != 2 || cands[0] != "127.0.0.1:9222" || cands[1] != "127.0.0.1:9323" {
		t.Errorf("listenCandidates(9222) = %v, want [127.0.0.1:9222 127.0.0.1:9323]", cands)
	}
}

func TestListenCandidates_EnvOverrideDisablesFallback(t *testing.T) {
	t.Setenv(ExtensionPortEnv, "9500")
	cands, err := listenCandidates("127.0.0.1:9222")
	if err != nil {
		t.Fatalf("listenCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0] != "127.0.0.1:9500" {
		t.Errorf("listenCandidates with env override = %v, want [127.0.0.1:9500] only", cands)
	}
}

func TestListenCandidates_NonDefaultPortNoFallback(t *testing.T) {
	t.Setenv(ExtensionPortEnv, "")
	cands, err := listenCandidates("127.0.0.1:9999")
	if err != nil {
		t.Fatalf("listenCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0] != "127.0.0.1:9999" {
		t.Errorf("listenCandidates(9999) = %v, want [127.0.0.1:9999] only", cands)
	}
}

// freePort reserves an ephemeral port and releases it. The tiny race between
// release and reuse is standard test practice.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestTryListen_FallsBackOnBusyPort verifies the EADDRINUSE path: when the
// primary candidate is held, the next candidate is bound and reported.
func TestTryListen_FallsBackOnBusyPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold busy port: %v", err)
	}
	defer busy.Close()
	free := freePort(t)

	l, bound, err := tryListen([]string{
		"127.0.0.1:" + strconv.Itoa(busy.Addr().(*net.TCPAddr).Port),
		"127.0.0.1:" + strconv.Itoa(free),
	})
	if err != nil {
		t.Fatalf("tryListen: %v", err)
	}
	defer l.Close()
	want := "127.0.0.1:" + strconv.Itoa(free)
	if bound != want {
		t.Errorf("bound = %q, want %q", bound, want)
	}
}

// TestTryListen_AllBusyFails verifies both candidates busy surfaces an error
// instead of silently succeeding.
func TestTryListen_AllBusyFails(t *testing.T) {
	b1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold port 1: %v", err)
	}
	defer b1.Close()
	b2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold port 2: %v", err)
	}
	defer b2.Close()

	l, _, err := tryListen([]string{
		"127.0.0.1:" + strconv.Itoa(b1.Addr().(*net.TCPAddr).Port),
		"127.0.0.1:" + strconv.Itoa(b2.Addr().(*net.TCPAddr).Port),
	})
	if err == nil {
		l.Close()
		t.Fatal("tryListen with both ports busy should fail, got a listener")
	}
}

// TestStart_EnvOverrideBindsEnvPort drives the full Start path with an env
// override: the server must serve health on the env port (never touching
// the possibly-occupied default 9222 of the host running the tests).
func TestStart_EnvOverrideBindsEnvPort(t *testing.T) {
	// Isolate the home dir so the token write never touches the real
	// ~/.monoagent state (withTempHome convention from token_test.go).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	port := freePort(t)
	t.Setenv(ExtensionPortEnv, strconv.Itoa(port))

	srv := NewServer("127.0.0.1:9222", zerolog.Nop())
	errCh := srv.StartAsync(context.Background())
	defer srv.Close() //nolint:errcheck

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(3 * time.Second)
	for !Probe(base) {
		if time.Now().After(deadline) {
			t.Fatalf("server never became healthy at %s", base)
		}
		time.Sleep(50 * time.Millisecond)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Start errored: %v", err)
	default:
	}
}
