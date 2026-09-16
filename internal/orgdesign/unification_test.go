package orgdesign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func loadGolden(t *testing.T, name string) (*Doc, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "orgs", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	return &d, raw
}

// Every unification key — including one nobody models yet
// (autonomy.future_key) — survives Load → Save unchanged.
func TestUnificationKeysRoundTrip(t *testing.T) {
	for _, name := range []string{"growth", "hq", "sales"} {
		t.Run(name, func(t *testing.T) {
			d, raw := loadGolden(t, name)
			if err := Validate(d); err != nil {
				t.Fatalf("golden %s invalid: %v", name, err)
			}
			root := t.TempDir()
			if _, err := Save(root, d); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(filepath.Join(root, ".monomind", "orgs", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var want, got interface{}
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(saved, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("round trip changed %s:\nwant %s\ngot  %s", name, raw, saved)
			}
		})
	}
}

func TestUnificationKeysAreTyped(t *testing.T) {
	d, _ := loadGolden(t, "growth")
	if d.Kind != OrgKindStandard || len(d.Automations) != 1 || d.Autonomy == nil || d.Federation == nil {
		t.Fatalf("doc keys not typed: %+v", d)
	}
	for _, k := range []string{"kind", "automations", "autonomy", "federation"} {
		if _, ok := d.Extra[k]; ok {
			t.Errorf("%s left in Extra", k)
		}
	}
	lead, _ := d.FindRole("lead")
	if len(lead.Automations) != 1 || len(lead.ToolProviders) != 1 {
		t.Fatalf("role keys not typed: %+v", lead)
	}
	if got := lead.PolicyStrings("approvalTools"); len(got) != 1 || got[0] != "monoagent__automation_publish_post" {
		t.Errorf("approvalTools = %v", got)
	}
	bot, _ := d.FindRole("publisher-bot")
	if !bot.IsEndpoint() || bot.Endpoint == nil || bot.Automation == nil || bot.Automation.WorkflowID != "wf-publish" {
		t.Fatalf("endpoint role not typed: %+v", bot)
	}
	if d.Autonomy.Extra["future_key"] == nil {
		t.Error("unknown nested key dropped")
	}
}

func TestSetPolicyStringsKeepsOtherPolicyKeys(t *testing.T) {
	r := Role{ID: "x", Extra: map[string]json.RawMessage{"policy": json.RawMessage(`{"git":"read","denyTools":["Bash"]}`)}}
	r.SetPolicyStrings("approvalTools", []string{"b", "a", "a"})
	if got := r.PolicyStrings("approvalTools"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("approvalTools = %v", got)
	}
	if !strings.Contains(string(r.Extra["policy"]), `"git":"read"`) {
		t.Fatalf("git key lost: %s", r.Extra["policy"])
	}
	r.SetPolicyStrings("approvalTools", nil)
	if strings.Contains(string(r.Extra["policy"]), "approvalTools") {
		t.Fatalf("approvalTools not removed: %s", r.Extra["policy"])
	}
}

func mustInvalid(t *testing.T, d *Doc, wantSubstr string) {
	t.Helper()
	err := Validate(d)
	if err == nil {
		t.Fatalf("expected a validation error containing %q", wantSubstr)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("error %q does not contain %q", err, wantSubstr)
	}
}

func TestValidateUnification(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(d *Doc)
		want   string
	}{
		{"alias shadows role", func(d *Doc) { d.Automations[0].Alias = "lead" }, "is also a role id"},
		{"reserved alias", func(d *Doc) { d.Automations[0].Alias = "status" }, "alias \"status\""},
		{"duplicate alias", func(d *Doc) { d.Automations = append(d.Automations, d.Automations[0]) }, "duplicate alias"},
		{"grant of unknown automation", func(d *Doc) { d.Roles[0].Automations[0].Alias = "nope" }, "is not in this org's automations"},
		{"bad grant mode", func(d *Doc) { d.Roles[0].Automations[0].Mode = "fly" }, "mode \"fly\""},
		{"endpoint root", func(d *Doc) {
			d.Roles[1].ReportsTo = nil
			d.Roles[0].ReportsTo = strPtr("publisher-bot")
			d.Roles[0].Type = "specialist"
		}, "cannot be the org root"},
		{"endpoint with policy", func(d *Doc) {
			d.Roles[1].Extra = map[string]json.RawMessage{"policy": json.RawMessage(`{}`)}
		}, "may not set policy"},
		{"endpoint with runtime", func(d *Doc) {
			d.Roles[1].Extra = map[string]json.RawMessage{"runtime": json.RawMessage(`"codex"`)}
		}, "may not set runtime"},
		{"endpoint without url", func(d *Doc) { d.Roles[1].Endpoint.URL = "" }, "needs endpoint.url"},
		{"endpoint workflow not member", func(d *Doc) { d.Roles[1].Automation.WorkflowID = "other" }, "not in this org's automations"},
		{"endpoint keys on agent role", func(d *Doc) { d.Roles[0].Endpoint = &Endpoint{URL: "http://x"} }, "only allowed when kind is \"endpoint\""},
		{"provider kind", func(d *Doc) { d.Roles[0].ToolProviders[0].Kind = "http" }, "kind must be mcp-stdio"},
		{"children on standard org", func(d *Doc) { d.ChildOrgs = []ChildOrg{{Org: "x"}} }, "only allowed when kind is \"holding\""},
		{"holding without children", func(d *Doc) { d.Kind = OrgKindHolding }, "needs at least one entry in children"},
		{"bad level", func(d *Doc) { d.Autonomy.Level = "turbo" }, "level \"turbo\""},
		{"boss fallback", func(d *Doc) { d.Autonomy.Decider.Fallback = "boss" }, "fallback cannot be boss"},
		{"unknown tier class", func(d *Doc) { d.Autonomy.Tiers = map[string]string{"everything": "routine"} }, "not a decision class"},
		{"bad tier", func(d *Doc) { d.Autonomy.Tiers = map[string]string{"gate": "meh"} }, "must be routine, consequential, or irreversible"},
		{"federation name", func(d *Doc) { d.Federation.AllowTo = []string{"../x"} }, "not an org name"},
		{"bad kind", func(d *Doc) { d.Kind = "conglomerate" }, "kind \"conglomerate\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := loadGolden(t, "growth")
			tc.mutate(d)
			mustInvalid(t, d, tc.want)
		})
	}
}

func TestValidDecisionClass(t *testing.T) {
	for _, c := range []string{"tool:Bash", "tool:*", "grant:publish_post", "grant:*", "hil:publish_post", "gate", "question", "org_start", "org_complete"} {
		if !ValidDecisionClass(c) {
			t.Errorf("%q should be valid", c)
		}
	}
	for _, c := range []string{"", "tool:", "hil:*x y", "gates", "grant"} {
		if ValidDecisionClass(c) {
			t.Errorf("%q should be invalid", c)
		}
	}
}

func TestValidateProfileOrgs(t *testing.T) {
	hq, _ := loadGolden(t, "hq")
	growth, _ := loadGolden(t, "growth")
	sales, _ := loadGolden(t, "sales")
	if errs := ValidateProfileOrgs([]*Doc{hq, growth, sales}); len(errs) != 0 {
		t.Fatalf("golden set invalid: %v", errs)
	}

	// sales used as decider parent without an owner.
	if errs := ValidateProfileOrgs([]*Doc{growth, sales}); len(errs) != 1 || !strings.Contains(errs[0], `decider "parent"`) {
		t.Fatalf("want parent-decider error, got %v", errs)
	}

	// Second holding org claiming sales (C-39) and a cycle hq -> sales -> hq.
	hq2 := &Doc{Name: "hq2", Kind: OrgKindHolding, ChildOrgs: []ChildOrg{{Org: "sales"}}}
	sales.Kind = OrgKindHolding
	sales.ChildOrgs = []ChildOrg{{Org: "hq"}}
	errs := ValidateProfileOrgs([]*Doc{hq, growth, sales, hq2})
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "already belongs to holding org") {
		t.Errorf("missing single-owner error: %v", errs)
	}
	if !strings.Contains(joined, "circular holding orgs") {
		t.Errorf("missing cycle error: %v", errs)
	}

	missing := &Doc{Name: "solo", Kind: OrgKindHolding, ChildOrgs: []ChildOrg{{Org: "ghost"}}}
	if errs := ValidateProfileOrgs([]*Doc{missing}); len(errs) != 1 || !strings.Contains(errs[0], "does not exist") {
		t.Fatalf("want missing-child error, got %v", errs)
	}
}
