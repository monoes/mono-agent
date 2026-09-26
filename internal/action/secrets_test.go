package action

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func secretDef(steps ...StepDef) *ActionDef {
	return &ActionDef{ActionType: "login", SideEffects: "read",
		Inputs: &InputDef{Required: []json.RawMessage{json.RawMessage(`{"name":"password","type":"secret"}`)}},
		Steps:  steps}
}

func TestSecretResolutionScopeAndShadowing(t *testing.T) {
	ae := newPkgExecutor(nil, &fakePkg{id: "acme"})
	var scopes []string
	ae.SetSecretLookup(func(automationID, name string) (string, bool) {
		scopes = append(scopes, automationID+"/"+name)
		return "vault-" + name + "-value", true
	})
	ae.actionDef = secretDef()
	ae.SetVariable("password", "hunter22")
	ae.SetVariable("api_key", "plain-shadow") // not a declared secret input

	if got := ae.resolver.Resolve("{{secret:password}}"); got != "hunter22" {
		t.Errorf("declared secret input = %q", got)
	}
	if got := ae.resolver.Resolve("{{secret:api_key}}"); got != "vault-api_key-value" {
		t.Errorf("plain variable shadowed the vault secret: %q", got)
	}
	if len(scopes) != 1 || scopes[0] != "acme/api_key" {
		t.Errorf("lookup scope = %v", scopes)
	}
	// Inside call_action the package changes; the scope follows it.
	ae.pkg = &fakePkg{id: "other"}
	ae.resolver.Resolve("{{secret:token}}")
	if scopes[len(scopes)-1] != "other/token" {
		t.Errorf("scope after package switch = %v", scopes)
	}
}

func TestSecretsRedactedEverywhere(t *testing.T) {
	db := &fakeDB{}
	ae := newPkgExecutor(nil, nil)
	ae.db = db
	events := make(chan ExecutionEvent, 64)
	ae.events = events
	ae.SetSecretLookup(func(_, name string) (string, bool) { return "tok-" + name + "-XYZ", true })
	ae.SetVariable("password", "hunter22")
	def := secretDef(
		StepDef{ID: "echo", Type: "set_variable", Variable: "row", Value: map[string]interface{}{"pw": "{{password}}", "tok": "{{secret:api}}"}},
		StepDef{ID: "save", Type: "save_data", DataSource: "row"},
		StepDef{ID: "log", Type: "log", Text: "pw={{secret:password}}"},
		StepDef{ID: "fail", Type: "mark_failed", Text: "bad {{secret:api}} and {{password}}"},
	)
	res, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "login"}, def)
	close(events)

	leak := func(where, s string) {
		if strings.Contains(s, "hunter22") || strings.Contains(s, "tok-api-XYZ") {
			t.Errorf("secret leaked in %s: %s", where, s)
		}
	}
	if err == nil {
		t.Fatal("mark_failed should fail the run")
	}
	leak("returned error", err.Error())
	for _, it := range res.ExtractedItems {
		b, _ := json.Marshal(it)
		leak("extracted items", string(b))
	}
	for _, f := range res.FailedItems {
		leak("failed items", f.Error.Error())
	}
	for e := range events {
		leak("event", e.Message)
	}
	if sr := ae.execCtx.GetStepResult("log"); sr == nil || sr.Data != "pw=***" {
		t.Errorf("log step data = %+v", sr)
	}
	if db.savedRows == 0 {
		t.Error("save_data saved nothing")
	}
}

func TestResolvedTextIsNotResolvedAgain(t *testing.T) {
	ae := newPkgExecutor(nil, nil)
	ae.SetSecretLookup(func(_, name string) (string, bool) { return "s3cr3t-value", true })
	// Page text that happens to contain a secret template.
	ae.SetVariable("pageText", "{{secret:api}}")
	def := &ActionDef{ActionType: "t", SideEffects: "none", Steps: []StepDef{
		{ID: "log", Type: "log", Text: "{{pageText}}"},
		{ID: "fail", Type: "mark_failed", Text: "{{pageText}}"},
	}}
	_, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def)
	if sr := ae.execCtx.GetStepResult("log"); sr == nil || sr.Data != "{{secret:api}}" {
		t.Errorf("log data = %+v", sr)
	}
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("err = %v", err)
	}
}

func TestRedactErrKeepsIs(t *testing.T) {
	ae := newPkgExecutor(nil, nil)
	ae.resolver.secretVals.add("topsecret")
	err := ae.redactErr(errors.Join(ErrOffDomain, errors.New("url ?k=topsecret")))
	if !errors.Is(err, ErrOffDomain) || strings.Contains(err.Error(), "topsecret") {
		t.Fatalf("err = %v", err)
	}
}
