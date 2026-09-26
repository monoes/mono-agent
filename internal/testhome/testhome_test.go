package testhome

import (
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

func TestHomeIsATempDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(home, "monoagent-testhome-") {
		t.Fatalf("HOME = %q, want the temp test home", home)
	}
	if os.Getenv("GOCACHE") == "" || strings.HasPrefix(os.Getenv("GOCACHE"), home) {
		t.Fatalf("GOCACHE = %q, want the real build cache", os.Getenv("GOCACHE"))
	}
}
