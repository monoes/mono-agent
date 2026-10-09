package orgdesign

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A sections org takes a schedule (monomind 2.24+): every tick is a fresh run
// with its own document store. Setting it must pass Validate and Save, keep
// the sections block, and write only the schedule line differently.
func TestSetScheduleOnSectionsOrgSavesAndKeepsSections(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".monomind", "orgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sec-org.json"), []byte(sectionsOrg), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Load(root, "sec-org")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetSchedule("30m"); err != nil {
		t.Fatalf("a sections org may be scheduled: %v", err)
	}
	if err := Validate(d); err != nil {
		t.Fatalf("Validate must not block a scheduled sections org: %v", err)
	}
	if _, err := Save(root, d); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "sec-org.json"))
	want := strings.Replace(sectionsOrg, `"schedule": null`, `"schedule": "30m"`, 1) + "\n"
	if string(got) != want && string(got) != strings.TrimSuffix(want, "\n") {
		t.Fatalf("only the schedule line may change:\n%s", got)
	}
	if err := d.SetSchedule(""); err != nil {
		t.Fatal(err)
	}
	if string(d.Schedule) != "null" {
		t.Fatalf("cleared schedule is null, got %s", d.Schedule)
	}
}

// Sections orgs are GA since monomind 2.24.0: a definition that already
// carries a schedule and no run_config.experimental flag validates as is.
func TestScheduledSectionsOrgNeedsNoExperimentalFlag(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".monomind", "orgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(sectionsOrg, `"schedule": null`, `"schedule": "15m"`, 1)
	if strings.Contains(src, "experimental") {
		t.Fatal("the fixture must not set run_config.experimental")
	}
	if err := os.WriteFile(filepath.Join(dir, "sec-org.json"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Load(root, "sec-org")
	if err != nil {
		t.Fatal(err)
	}
	if !d.SectionsEnabled() || string(d.Schedule) != `"15m"` {
		t.Fatalf("sections=%v schedule=%s", d.SectionsEnabled(), d.Schedule)
	}
	if err := Validate(d); err != nil {
		t.Fatalf("a scheduled sections org without the experimental flag must validate: %v", err)
	}
}

func TestSetScheduleValidatesMonomindsFormat(t *testing.T) {
	for in, ok := range map[string]bool{
		"15m": true, "2h": true, "45s": true, " 10m ": true, "": true, "90": true,
		"daily": false, "0 * * * *": false, "m": false, "-5m": false, "1.5h": false, "0m": false, "0": false,
	} {
		d := &Doc{}
		err := d.SetSchedule(in)
		if (err == nil) != ok {
			t.Errorf("SetSchedule(%q) err=%v, want ok=%v", in, err, ok)
		}
	}
}

// An interval too large for monomind's timer (it overflows Node's 2^31-1 ms
// setInterval limit and would fire every millisecond) or for an int is refused
// with a message that names the real problem.
func TestSetScheduleRejectsOversizedIntervals(t *testing.T) {
	for _, s := range []string{"99999999999999999999", "99999999999999999999m", "1000h", "3000000s", "40000"} {
		d := &Doc{}
		err := d.SetSchedule(s)
		if err == nil || !strings.Contains(err.Error(), "too long") {
			t.Errorf("SetSchedule(%q) = %v, want a too-long refusal", s, err)
		}
	}
	for _, s := range []string{"1s", "500h", "30000"} {
		if err := (&Doc{}).SetSchedule(s); err != nil {
			t.Errorf("SetSchedule(%q) = %v, want accepted", s, err)
		}
	}
}
