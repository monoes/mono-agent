package orgdecide

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update-route-golden", false, "rewrite testdata/route_golden.txt")

// TestRouteGolden pins ClassForAction, TierFor, Route, and DefaultTiers to
// the behaviour recorded before the jev decider existed (plan D5: security
// policy is never delegated). Any diff here is a policy change, not a
// refactor; regenerate only on purpose with -update-route-golden.
func TestRouteGolden(t *testing.T) {
	var b strings.Builder
	keys := make([]string, 0, len(DefaultTiers))
	for k := range DefaultTiers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "default %s=%s\n", k, DefaultTiers[k])
	}
	for _, a := range []string{"Bash", "Read", "WebFetch", "org_complete", "monoagent__org_start", "monoagent__automation_post",
		"monoagent__automation_", "mcp__org__monoagent__org_start", "", "org_start"} {
		fmt.Fprintf(&b, "class %q=%s\n", a, ClassForAction(a))
	}
	factSets := map[string]TierFacts{
		"none": {},
		"some": {
			GrantTier: func(alias string) string {
				return map[string]string{"post": "irreversible", "sum": "consequential", "read": "routine", "bad": "weird"}[alias]
			},
			RoleHasGrantsAndBash: func(role string) bool { return role == "writer" },
		},
	}
	overrideSets := map[string]map[string]string{
		"none": nil,
		"wild": {"tool:*": "consequential", "grant:*": "routine"},
		"exact": {"tool:Read": "irreversible", "gate": "consequential", "question": "routine", "hil:post": "routine",
			"org_complete": "bogus", "tool:*": "irreversible"},
	}
	classes := []string{"tool:Bash", "tool:Read", "tool:WebFetch", "org_complete", "question", "org_start", "gate",
		"grant:post", "grant:sum", "grant:read", "grant:bad", "grant:unknown", "hil:post", "hil:sum", "hil:unknown", "mystery", ""}
	for _, fk := range []string{"none", "some"} {
		for _, ok := range []string{"none", "wild", "exact"} {
			for _, c := range classes {
				for _, role := range []string{"dev", "writer"} {
					fmt.Fprintf(&b, "tier facts=%s over=%s %s/%s=%s\n", fk, ok, c, role, TierFor(c, role, overrideSets[ok], factSets[fk]))
				}
			}
		}
	}
	for _, level := range []string{"manual", "mid", "full", "", "bogus"} {
		for _, tier := range []string{"routine", "consequential", "irreversible", "", "bogus"} {
			fmt.Fprintf(&b, "route %s/%s=%s\n", level, tier, Route(level, tier))
		}
	}
	path := filepath.Join("testdata", "route_golden.txt")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != string(want) {
		t.Fatalf("routing policy changed (plan D5); got:\n%s", got)
	}
}
