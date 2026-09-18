package orggrant

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func insertAutonomyFor(t *testing.T, db *sql.DB, profile, org string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO org_autonomy (profile_id, org_name, level, decider_json, updated_at, updated_by)
		 VALUES (?, ?, 'full', '{"kind":"model"}', '2026-09-18T10:00:00Z', 'cli')`, profile, org); err != nil {
		t.Fatal(err)
	}
}

func insertDelegationFor(t *testing.T, db *sql.DB, id, profile, org string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO org_delegations (id, profile_id, org_name, item_kind, item_ref, item_json, level, decider_org, decider_role, status, created_at, deadline_at)
		 VALUES (?, ?, ?, 'approval', 'appr-1', '{}', 'mid', ?, 'lead', 'pending', '2026-09-18T10:00:00Z', '2026-09-18T11:00:00Z')`,
		id, profile, org, org); err != nil {
		t.Fatal(err)
	}
}

// seedTwoProfiles gives profiles "work" and "home" one of everything that
// grants power: an automation grant, an org-tool grant, a live endpoint, a
// rotated-away endpoint still inside its grace window, an autonomy row,
// and a pending delegation.
func seedTwoProfiles(t *testing.T, s *Store, db *sql.DB) map[string]string {
	t.Helper()
	ctx := context.Background()
	leaked := map[string]string{}
	for _, p := range []string{"work", "home"} {
		org := "growth-" + p
		if _, err := s.UpsertGrant(ctx, GrantInput{ProfileID: p, OrgName: org, RoleID: "lead", Tool: Tool{Alias: "publish", WorkflowID: "wf-" + p}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetOrgTools(ctx, p, org, "lead", []OrgTool{{Tool: "decision_list", Orgs: []string{org}}}); err != nil {
			t.Fatal(err)
		}
		old, err := s.CreateEndpoint(ctx, p, org, "bot", "wf-"+p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.RotateEndpoint(ctx, p, org, "bot"); err != nil {
			t.Fatal(err)
		}
		leaked[p] = old.ID
		insertAutonomyFor(t, db, p, org)
		insertDelegationFor(t, db, "d-"+p, p, org)
	}
	return leaked
}

// C-24: deleting a profile must leave nothing of its orgs that still grants
// power — no grant a leftover provider could resolve, no endpoint id
// (rotated-away ones in their grace window included) that starts runs, no
// autonomy row a new profile of the same id would inherit, no delegation a
// decider could still be asked about — and must not touch other profiles.
func TestRevokeProfileClearsOnlyThatProfile(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	s.now = func() time.Time { return now }
	leaked := seedTwoProfiles(t, s, db)

	preview, err := s.ProfileFootprint(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	want := ProfileRevocation{Grants: 2, Endpoints: 2, Autonomy: 1, Delegations: 1}
	if preview != want {
		t.Fatalf("footprint = %+v, want %+v", preview, want)
	}

	got, err := s.RevokeProfile(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("revoked = %+v, want %+v", got, want)
	}

	if g, _ := s.ListProfileGrants(ctx, "work"); len(g) != 0 {
		t.Fatalf("work grants survived: %d", len(g))
	}
	if _, err := s.LookupEndpoint(ctx, leaked["work"]); err != ErrNotFound {
		t.Fatalf("rotated-away work endpoint still accepted: %v", err)
	}
	if _, err := s.LiveEndpoint(ctx, "work", "growth-work", "bot"); err != ErrNotFound {
		t.Fatalf("live work endpoint survived: %v", err)
	}
	if got := delegationStatus(t, db, "d-work"); got != "expired" {
		t.Fatalf("work delegation = %q", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM org_autonomy WHERE profile_id = 'work'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("work autonomy rows survived: %d", n)
	}

	after, err := s.ProfileFootprint(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	if after != (ProfileRevocation{}) {
		t.Fatalf("footprint after revoke = %+v", after)
	}
	again, err := s.RevokeProfile(ctx, "work")
	if err != nil || again != (ProfileRevocation{}) {
		t.Fatalf("second revoke = %+v, %v; want a no-op", again, err)
	}

	home, err := s.ProfileFootprint(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	if home != want {
		t.Fatalf("home footprint changed: %+v", home)
	}
	if _, err := s.LookupEndpoint(ctx, leaked["home"]); err != nil {
		t.Fatalf("home's grace-window endpoint was revoked: %v", err)
	}
}

func TestRevokeProfileRequiresID(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.RevokeProfile(context.Background(), ""); err == nil {
		t.Fatal("empty profile id accepted")
	}
	if _, err := s.ProfileFootprint(context.Background(), " "); err == nil {
		t.Fatal("blank profile id accepted")
	}
}
