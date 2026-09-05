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
