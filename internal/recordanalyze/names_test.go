package recordanalyze

import "testing"

func TestSlugs(t *testing.T) {
	if got := AutomationSlug("Acme CRM!"); got != "acme-crm" {
		t.Errorf("AutomationSlug = %q", got)
	}
	if got := ActionSlug("Create a Contact"); got != "create_a_contact" {
		t.Errorf("ActionSlug = %q", got)
	}
	if got := InputSlug("2fa code"); got != "v_2fa_code" {
		t.Errorf("InputSlug = %q", got)
	}
	if got := ActionSlug("!!!"); got != "recorded_action" {
		t.Errorf("fallback = %q", got)
	}
	taken := map[string]bool{"x": true, "x_2": true}
	if got := Unique("x", "_", func(s string) bool { return taken[s] }); got != "x_3" {
		t.Errorf("Unique = %q", got)
	}
	if !ValidAutomationID("acme-crm") || ValidAutomationID("Acme") || !ValidActionName("a_b") || ValidActionName("a-b") {
		t.Error("validators")
	}
}
