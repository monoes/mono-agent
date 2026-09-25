package nodes

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

// fakeVault swaps the vault for a map keyed by "<profile>|<name>".
func fakeVault(t *testing.T, entries map[string]string) *[]string {
	t.Helper()
	var asked []string
	prev := resolveSecret
	resolveSecret = func(_ context.Context, _ *sql.DB, profileID, name string) (string, error) {
		asked = append(asked, name)
		if v, ok := entries[profileID+"|"+name]; ok {
			return v, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { resolveSecret = prev })
	return &asked
}

func TestSecretLookup_NamespacedThenBare(t *testing.T) {
	fakeVault(t, map[string]string{
		"p1|acme-crm/api_key": "namespaced",
		"p1|shared_token":     "bare",
		"p2|acme-crm/api_key": "other-profile",
	})
	look := secretLookup(context.Background(), nil, "p1", "acme-crm")

	if v, ok := look("api_key"); !ok || v != "namespaced" {
		t.Errorf("api_key = %q %v, want the namespaced value", v, ok)
	}
	if v, ok := look("shared_token"); !ok || v != "bare" {
		t.Errorf("shared_token = %q %v, want the bare fallback", v, ok)
	}
	if _, ok := look("missing"); ok {
		t.Error("missing secret resolved")
	}
	if _, ok := look("  "); ok {
		t.Error("empty name resolved")
	}
}

// An imported package reads only secrets filed under its own id.
func TestSecretLookup_ImportedPackageNoBareFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	reg, err := BootAutomations(filepath.Join(home, ".monoagent"))
	t.Cleanup(func() { action.SetDefSource(nil) })
	if err != nil {
		t.Fatalf("BootAutomations: %v", err)
	}
	src := filepath.Join(t.TempDir(), "acme-crm")
	writeFile(t, filepath.Join(src, "automation.json"), `{
	  "schema": "monoagent.automation/v1",
	  "id": "acme-crm", "name": "Acme CRM", "version": "1.0.0",
	  "site": {"startUrl": "https://app.acme-crm.com/", "domains": ["app.acme-crm.com"]},
	  "permissions": {"steps": ["navigate"], "scripts": [], "downloads": false},
	  "actions": ["open"],
	  "policy": {"tier": "standard"}
	}`)
	writeFile(t, filepath.Join(src, "actions", "open.json"), `{
	  "actionType": "open", "automation": "acme-crm", "sideEffects": "none",
	  "steps": [{"id": "open", "type": "navigate", "url": "https://app.acme-crm.com/"}]
	}`)
	if res, err := reg.Install(src, automation.InstallOptions{}); err != nil || !res.Installed {
		t.Fatalf("Install: %v %+v", err, res)
	}

	asked := fakeVault(t, map[string]string{
		"p1|acme-crm/api_key": "namespaced",
		"p1|github_token":     "must-not-leak",
	})
	look := secretLookup(context.Background(), nil, "p1", "acme-crm")
	if v, ok := look("api_key"); !ok || v != "namespaced" {
		t.Errorf("api_key = %q %v", v, ok)
	}
	if v, ok := look("github_token"); ok {
		t.Errorf("imported package read a bare vault secret: %q", v)
	}
	for _, n := range *asked {
		if n == "github_token" {
			t.Error("bare name was even looked up for an imported package")
		}
	}

	// A built-in keeps the bare fallback.
	builtin := secretLookup(context.Background(), nil, "p1", "gemini")
	if _, ok := builtin("github_token"); !ok {
		t.Error("built-in automation lost the bare-name fallback")
	}
}
