package automation

import (
	"errors"
	"strings"
	"testing"
)

// L1: identical content is a no-op whatever trust it arrives with.
func TestIdenticalReimportIsNoOpAndKeepsTrust(t *testing.T) {
	r := newReg(t)
	if _, err := r.AddAction("acme-crm", mustOpenDir(t, acmeDir(t)), "create_contact", InstallOptions{Trust: TrustRecorded}); err != nil {
		t.Fatal(err)
	}
	info, _ := r.Info("acme-crm")
	g := generation(t, r)

	// The same action again, from an imported .mpkg: no --replace needed,
	// nothing written, trust stays recorded.
	imp, err := OpenFile(packDir(t, acmeDir(t)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.AddAction("acme-crm", imp, "create_contact", InstallOptions{})
	if err != nil || !strings.Contains(strings.Join(res.Warnings, "\n"), "no changes") {
		t.Fatalf("identical re-import: %v %+v", err, res)
	}
	after, _ := r.Info("acme-crm")
	if after.Trust != TrustRecorded || after.Version != info.Version || generation(t, r) != g {
		t.Errorf("identical re-import changed things: %+v", after)
	}
}

// Upgrading your own local package from a directory to a newer version
// needs no --replace; the other replacements still do.
func TestOwnLocalUpgradeNeedsNoReplace(t *testing.T) {
	r := newReg(t)
	if _, err := r.Install(acmeDir(t), InstallOptions{Trust: TrustLocal}); err != nil {
		t.Fatal(err)
	}
	files := acmeFiles()
	files["automation.json"] = strings.Replace(files["automation.json"], `"1.2.0"`, `"1.3.0"`, 1)
	files["scripts/parse.js"] = "return 'v2';\n"
	newer := writeTree(t, t.TempDir(), files)

	res, err := r.Install(newer, InstallOptions{Trust: TrustLocal, DryRun: true})
	if err != nil || res.Review.ReplaceRequired {
		t.Fatalf("dry run of own upgrade: %v replaceRequired=%v", err, res.Review.ReplaceRequired)
	}
	if _, err := r.Install(newer, InstallOptions{Trust: TrustLocal}); err != nil {
		t.Fatalf("own upgrade: %v", err)
	}
	info, _ := r.Info("acme-crm")
	if info.Version != "1.3.0" || info.PreviousVersion != "1.2.0" || info.Trust != TrustLocal {
		t.Fatalf("after upgrade: %+v", info)
	}
	if err := r.Rollback("acme-crm"); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.Info("acme-crm"); info.Version != "1.2.0" {
		t.Errorf("rollback: %+v", info)
	}
	r.Rollback("acme-crm") // back to 1.3.0

	// Still confirmed: same version different content …
	files["scripts/parse.js"] = "return 'v3';\n"
	same := writeTree(t, t.TempDir(), files)
	if res, err := r.Install(same, InstallOptions{Trust: TrustLocal}); !errors.Is(err, ErrReplaces) || !res.Review.ReplaceRequired {
		t.Errorf("same-version change: %v", err)
	}
	// … a downgrade …
	older := acmeFiles()
	older["automation.json"] = strings.Replace(older["automation.json"], `"1.2.0"`, `"1.1.0"`, 1)
	if _, err := r.Install(writeTree(t, t.TempDir(), older), InstallOptions{Trust: TrustLocal}); !errors.Is(err, ErrReplaces) {
		t.Errorf("downgrade of own package: %v", err)
	}
	// … a trust drop (newer version as recorded) …
	files["automation.json"] = strings.Replace(files["automation.json"], `"1.3.0"`, `"1.4.0"`, 1)
	newest := writeTree(t, t.TempDir(), files)
	if _, err := r.Install(newest, InstallOptions{Trust: TrustRecorded}); !errors.Is(err, ErrReplaces) {
		t.Errorf("trust drop: %v", err)
	}
	// … and imported over own.
	if _, err := r.Install(packDir(t, newest), InstallOptions{}); !errors.Is(err, ErrReplaces) {
		t.Errorf("imported over own: %v", err)
	}
	// A recorded own package upgraded to local from a directory still asks.
	rec := newReg(t)
	rec.AddAction("acme-crm", mustOpenDir(t, acmeDir(t)), "create_contact", InstallOptions{Trust: TrustRecorded})
	if _, err := rec.Install(newest, InstallOptions{Trust: TrustLocal}); !errors.Is(err, ErrReplaces) {
		t.Errorf("recorded package replaced by a local dir without confirmation: %v", err)
	}
}
