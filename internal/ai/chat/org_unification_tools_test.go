package chat

import (
	"context"
	"strings"
	"testing"
)

func TestSetOrgGrant_PreviewsUntilConfirmedThenShellsCLI(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "/bin/monoagentcli")
	mt.SetProfileID("p1")

	var gotArgs []string
	stubRunSelfExec(t, func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte("warning: something on stderr\n{\"v\":1,\"org\":\"growth\",\"grant\":{\"id\":\"grt_x\"}}\n"), nil
	})

	preview, err := mt.Execute("set_org_grant", `{"org_name":"growth","role_id":"lead","automation":"publish_post","approval":"required","wait":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview, "would_apply") || gotArgs != nil {
		t.Fatalf("expected preview without exec, got %s (args %v)", preview, gotArgs)
	}

	out, err := mt.Execute("set_org_grant", `{"org_name":"growth","role_id":"lead","automation":"publish_post","approval":"required","wait":false,"confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, `{"v":1`) {
		t.Fatalf("output = %q", out)
	}
	want := "--profile p1 org grant add growth --role lead --automation publish_post --wait=false --approval required"
	if strings.Join(gotArgs, " ") != want {
		t.Fatalf("args = %q, want %q", strings.Join(gotArgs, " "), want)
	}
}

func TestSetOrgGrant_RefusedAfterSyncedComms(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "/bin/monoagentcli")
	mt.markSyncedCommsSeen()
	stubRunSelfExec(t, func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		t.Fatal("exec must not run")
		return nil, nil
	})
	for _, tool := range []string{"set_org_grant", "set_org_autonomy"} {
		_, err := mt.Execute(tool, `{"org_name":"growth","role_id":"lead","automation":"a","level":"full","confirm":true}`)
		if err == nil || !strings.Contains(err.Error(), "synced communications") {
			t.Fatalf("%s: err = %v", tool, err)
		}
	}
}
