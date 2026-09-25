package main

import (
	"reflect"
	"testing"
)

func TestAutomationCLIArgs(t *testing.T) {
	got := automationCLIArgs("p1", "automation", "list")
	want := []string{"--profile", "p1", "--json", "automation", "list"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := automationCLIArgs("", "record", "list"); !reflect.DeepEqual(got, []string{"--json", "record", "list"}) {
		t.Fatalf("no profile: got %v", got)
	}
}

func TestAutomationInstallArgs(t *testing.T) {
	dry, err := automationInstallArgs("/x/a.mpkg", true)
	if err != nil || !reflect.DeepEqual(dry, []string{"automation", "install", "/x/a.mpkg", "--dry-run"}) {
		t.Fatalf("dry run: %v %v", dry, err)
	}
	yes, err := automationInstallArgs("/x/a.mpkg", false)
	if err != nil || !reflect.DeepEqual(yes, []string{"automation", "install", "/x/a.mpkg", "--yes"}) {
		t.Fatalf("confirm: %v %v", yes, err)
	}
	if _, err := automationInstallArgs(" ", true); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestAutomationLifecycleArgs(t *testing.T) {
	got, err := automationLifecycleArgs("rollback", "hackernews")
	if err != nil || !reflect.DeepEqual(got, []string{"automation", "rollback", "hackernews"}) {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := automationLifecycleArgs("pack", "x"); err == nil {
		t.Fatal("unknown verb accepted")
	}
	if _, err := automationLifecycleArgs("enable", ""); err == nil {
		t.Fatal("empty id accepted")
	}
}

func TestAutomationTestAndExportArgs(t *testing.T) {
	got, _ := automationTestArgs("hn", "submit_post", true)
	if !reflect.DeepEqual(got, []string{"automation", "test", "hn", "submit_post", "--live"}) {
		t.Fatalf("test: %v", got)
	}
	got, _ = automationExportArgs("hn", "/o/hn.mpkg", false)
	if !reflect.DeepEqual(got, []string{"automation", "export", "hn", "-o", "/o/hn.mpkg"}) {
		t.Fatalf("export: %v", got)
	}
	got, err := actionExportArgs("hn.submit_post", "/o/a.mpkg")
	if err != nil || !reflect.DeepEqual(got, []string{"action", "export", "hn.submit_post", "-o", "/o/a.mpkg"}) {
		t.Fatalf("action export: %v %v", got, err)
	}
	if _, err := actionExportArgs("submit_post", "/o"); err == nil {
		t.Fatal("unqualified action ref accepted")
	}
}

func TestRecordSaveArgs(t *testing.T) {
	got, err := recordSaveArgs("/d", SaveDraftSpec{
		As: "action", Automation: "acme", New: "acme-crm", Name: "create_contact",
		RenameInputs: map[string]string{"b": "beta", "a": "alpha", "same": "same", "empty": ""},
	})
	want := []string{"record", "save", "/d", "--as", "action", "--new", "acme-crm", "--name", "create_contact",
		"--rename-input", "a=alpha", "--rename-input", "b=beta"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v %v\nwant %v", got, err, want)
	}
	got, _ = recordSaveArgs("/d", SaveDraftSpec{Automation: "acme"})
	if !reflect.DeepEqual(got, []string{"record", "save", "/d", "--automation", "acme"}) {
		t.Fatalf("existing: %v", got)
	}
	if _, err := recordSaveArgs("/d", SaveDraftSpec{As: "node"}); err == nil {
		t.Fatal("bad save-as accepted")
	}
}
