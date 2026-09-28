package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func fileSum(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestLibraryInstallTrustTiers(t *testing.T) {
	pkg := packDir(t, acmeDir(t))
	sum := fileSum(t, pkg)
	for _, tc := range []struct {
		name     string
		official bool
		want     string
	}{
		{"official", true, TrustBuiltin},
		{"community", false, TrustImported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReg(t)
			origin := &LibraryOrigin{ItemID: "it-1", Slug: "acme-crm", Version: "1.2.0", SHA256: sum, Official: tc.official}
			res, err := r.Install(pkg, InstallOptions{ExpectSHA256: sum, Library: origin})
			if err != nil {
				t.Fatalf("Install: %v", err)
			}
			if res.Review.Source != "" && res.Review.Source != SourceMonoes {
				t.Errorf("review source = %q", res.Review.Source)
			}
			info, err := r.Info("acme-crm")
			if err != nil {
				t.Fatal(err)
			}
			if info.Source != SourceMonoes || info.Trust != tc.want || info.Library == nil || info.Library.ItemID != "it-1" || info.Library.Source != SourceMonoes {
				t.Fatalf("info = %+v (library %+v)", info, info.Library)
			}
			if got := scriptsAllowed(info.Trust, nil); got != tc.official {
				t.Errorf("scripts allowed = %v", got)
			}
			// A reinstall from anywhere else drops the provenance.
			if _, err := r.Install(pkg, InstallOptions{Replace: true}); err != nil {
				t.Fatal(err)
			}
			if info, _ := r.Info("acme-crm"); info.Library != nil || info.Source != SourceImported {
				t.Fatalf("after plain reinstall: %+v", info)
			}
		})
	}
}

func TestLibraryInstallNeedsPinAndItem(t *testing.T) {
	r := newReg(t)
	pkg := packDir(t, acmeDir(t))
	if _, err := r.Install(pkg, InstallOptions{Library: &LibraryOrigin{ItemID: "x", Official: true}}); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("no pin: %v", err)
	}
	if _, err := r.Install(pkg, InstallOptions{ExpectSHA256: fileSum(t, pkg), Library: &LibraryOrigin{Official: true}}); err == nil {
		t.Fatal("no item id accepted")
	}
	if _, err := r.Install(pkg, InstallOptions{ExpectSHA256: strings.Repeat("0", 64), Library: &LibraryOrigin{ItemID: "x", Official: true}}); err == nil {
		t.Fatal("wrong pin accepted")
	}
	if _, err := r.Install(pkg, InstallOptions{Source: SourceMonoes}); err == nil {
		t.Fatal("source monoes without a library origin accepted")
	}
	if _, err := r.Install(pkg, InstallOptions{ExpectSHA256: fileSum(t, pkg), Trust: TrustBuiltin, Library: &LibraryOrigin{ItemID: "x"}}); err == nil {
		t.Fatal("a trust override on a library install accepted")
	}
}

func TestSetLibraryOriginAdoptsBuiltinsOnly(t *testing.T) {
	r := newReg(t)
	if err := r.Seed(seedFS("1.0.0", "a")); err != nil {
		t.Fatal(err)
	}
	o := &LibraryOrigin{ItemID: "it-9", Version: "1.0.0", Official: true}
	if ok, err := r.SetLibraryOrigin("demo", o); err != nil || !ok {
		t.Fatalf("adopt = %v, %v", ok, err)
	}
	if ok, _ := r.SetLibraryOrigin("demo", o); ok {
		t.Fatal("adopted twice")
	}
	info, _ := r.Info("demo")
	if info.Source != SourceBuiltin || info.Library == nil || info.Library.Source != SourceMonoes {
		t.Fatalf("info = %+v", info)
	}
	if _, err := r.Install(acmeDir(t), InstallOptions{Source: SourceLocal}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.SetLibraryOrigin("acme-crm", o); ok {
		t.Fatal("adopted the user's own package")
	}
}
