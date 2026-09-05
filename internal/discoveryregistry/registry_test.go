// internal/discoveryregistry/registry_test.go
package discoveryregistry_test

import (
	"testing"

	"github.com/monoes/mono-agent/internal/discoveryregistry"
)

func TestGetLinkedIn(t *testing.T) {
	src, ok := discoveryregistry.Get("linkedin")
	if !ok {
		t.Fatal("expected \"linkedin\" to be registered")
	}
	if src.Name() != "linkedin" {
		t.Fatalf("expected Name() linkedin, got %q", src.Name())
	}
}

func TestGetArbeitnow(t *testing.T) {
	src, ok := discoveryregistry.Get("arbeitnow")
	if !ok {
		t.Fatal("expected \"arbeitnow\" to be registered")
	}
	if src.Name() != "arbeitnow" {
		t.Fatalf("expected Name() arbeitnow, got %q", src.Name())
	}
}

func TestGetJobicy(t *testing.T) {
	src, ok := discoveryregistry.Get("jobicy")
	if !ok {
		t.Fatal("expected \"jobicy\" to be registered")
	}
	if src.Name() != "jobicy" {
		t.Fatalf("expected Name() jobicy, got %q", src.Name())
	}
}

func TestGetUnknown(t *testing.T) {
	_, ok := discoveryregistry.Get("does-not-exist")
	if ok {
		t.Fatal("expected unknown source name to return ok=false")
	}
}

func TestNamesIncludesLinkedIn(t *testing.T) {
	names := discoveryregistry.Names()
	found := false
	for _, n := range names {
		if n == "linkedin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Names() to include \"linkedin\", got %v", names)
	}
}
