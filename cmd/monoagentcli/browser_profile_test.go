package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	browserpkg "github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/extension"
	"github.com/monoes/mono-agent/internal/vault"
)

// fakeRouter records which profile each narrowed bridge was made for.
type fakeRouter struct {
	mu    sync.Mutex
	asked []string
}

func (f *fakeRouter) IsConnected() bool                    { return true }
func (f *fakeRouter) CreateTab(string) (int, error)        { return 1, nil }
func (f *fakeRouter) CloseTab(int) error                   { return nil }
func (f *fakeRouter) NewPage(int) browserpkg.PageInterface { return nil }
func (f *fakeRouter) ForProfile(p string) browserpkg.ExtensionBridge {
	f.mu.Lock()
	f.asked = append(f.asked, p)
	f.mu.Unlock()
	return &fakeNarrowed{profile: p}
}

type fakeNarrowed struct{ profile string }

func (f *fakeNarrowed) IsConnected() bool                    { return true }
func (f *fakeNarrowed) CreateTab(string) (int, error)        { return 1, nil }
func (f *fakeNarrowed) CloseTab(int) error                   { return nil }
func (f *fakeNarrowed) NewPage(int) browserpkg.PageInterface { return nil }

// plainBridge is a bridge that cannot route (e.g. a test double).
type plainBridge struct{ fakeNarrowed }

func TestBridgeForProfile(t *testing.T) {
	r := &fakeRouter{}
	if got := bridgeForProfile(r, "p-work"); got.(*fakeNarrowed).profile != "p-work" {
		t.Fatalf("router not narrowed: %+v", got)
	}
	if got := bridgeForProfile(r, ""); got != browserpkg.ExtensionBridge(r) {
		t.Fatal("an empty profile must leave the bridge alone")
	}
	p := &plainBridge{}
	if got := bridgeForProfile(p, "p-work"); got != browserpkg.ExtensionBridge(p) {
		t.Fatal("a bridge that cannot route is returned as is")
	}
}

func TestLazyProviderUsesTheRunsProfile(t *testing.T) {
	r := &fakeRouter{}
	p := &lazyBrowserSessionProvider{
		logger:         zerolog.Nop(),
		defaultProfile: "p-default",
		setup:          func() browserpkg.ExtensionBridge { return r },
		ensure:         func(connChecker, time.Duration) error { return nil },
	}
	if _, err := p.GetPage(vault.ContextWithProfileID(context.Background(), "p-run"), "instagram", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetPage(context.Background(), "instagram", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.asked, ",") != "p-run,p-default" {
		t.Fatalf("asked = %v", r.asked)
	}
}

func TestLazyProviderDoesNotMakeOneProfileWaitForAnother(t *testing.T) {
	r := &fakeRouter{}
	release := make(chan struct{})
	p := &lazyBrowserSessionProvider{
		logger: zerolog.Nop(),
		setup:  func() browserpkg.ExtensionBridge { return r },
		ensure: func(b connChecker, _ time.Duration) error {
			if b.(*fakeNarrowed).profile == "p-slow" {
				<-release // this profile's browser is still starting
			}
			return nil
		},
	}
	go func() { _, _ = p.GetPage(vault.ContextWithProfileID(context.Background(), "p-slow"), "x", "") }()
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	if _, err := p.GetPage(vault.ContextWithProfileID(context.Background(), "p-fast"), "x", ""); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("p-fast waited %s behind p-slow", d)
	}
	close(release)
}

type routedStub struct {
	fakeNarrowed
	err error
}

func (r *routedStub) Route() (extension.ConnInfo, error) { return extension.ConnInfo{}, r.err }

func TestRouteHintNamesTheFix(t *testing.T) {
	stub := &routedStub{err: &extension.NoBrowserError{Profile: "p-x", BoundTo: []string{"p-a"}}}
	if h := routeHint(stub); !strings.Contains(h, `"p-x"`) || !strings.Contains(h, "extension bind") {
		t.Fatalf("hint = %q", h)
	}
	if h := routeHint(&plainBridge{}); h != "" {
		t.Fatalf("no hint for a plain bridge, got %q", h)
	}
}
