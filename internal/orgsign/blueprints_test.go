package orgsign

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Blueprint digests (monomind#571/#575). The package digests and org hashes
// below are monomind 2.22.0's own (packageDigest and computeOrgDefHash from
// dist/src), computed over the same catalog; the first is the vector pinned
// in mono-agent#299.
const (
	bpSec = `{"name":"sec","description":"Reviews code","skills":["audit"]}`
	bpDev = "{\"name\":\"dev\",\"description\":\"Writes code é\",\"skills\":[]}\n"

	bpSecPackage = "df386b9ab4b147b92f0ae067043fc929635b2791d6f32aeb00c6bdcb2a76e70e"
	bpDevPackage = "66dcae57c0ecec6382dc20838fb21444463764476eb829476729ffe1f244e095"

	bpOrgPinned = `{"name":"fx","goal":"ship it","roles":[{"id":"boss","type":"boss","reports_to":null,"title":"CEO","blueprint":"sec"}]}`
	bpHashPined = "2db4cf8c4596e17c97bd20a66d30c56cdaccf6bf9fc71a4e57adbadf2d7e7508"
	bpOrgMany   = `{"name":"fx","roles":[{"id":"a","blueprint":"sec"},{"id":"b","blueprint":"dev"},{"id":"c","blueprint":"sec"}]}`
	bpHashMany  = "4b39562ca53674f7ea28561483b5a9dcd87041d1706c73148a6ab74d610ee1dd"
	// A non-string blueprint is no digest at all, as in monomind.
	bpOrgNonString = `{"name":"fx","roles":[{"id":"a","blueprint":"sec"},{"id":"c","blueprint":7}]}`
	bpHashNonStr   = "a7614479fcb41064a6cc80ff2757279ebf750a75c6f8dd35b957aedd275fe8fb"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// catalogRoot builds a project with catalog blueprints sec and dev active
// for org, packaged like monomind's catalog.
func catalogRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	pk := filepath.Join(root, ".monomind", "catalog", "packages")
	writeFile(t, filepath.Join(pk, "sec", bpSecPackage[:12], "blueprint.json"), bpSec)
	for f, c := range map[string]string{"blueprint.json": bpDev, "README.md": "hi", "z/a.txt": "x", "Z.txt": "y", "é.txt": "e"} {
		writeFile(t, filepath.Join(pk, "dev", bpDevPackage[:12], f), c)
	}
	writeCatalogState(t, root, func(e map[string]interface{}) {})
	return root
}

func writeCatalogState(t *testing.T, root string, edit func(entry map[string]interface{})) {
	t.Helper()
	var entries []interface{}
	for name, sha := range map[string]string{"sec": bpSecPackage, "dev": bpDevPackage} {
		e := map[string]interface{}{"id": "blueprint:" + name, "kind": "blueprint", "status": "active",
			"sha256": sha, "targets": []string{"org"}}
		if name == "sec" {
			edit(e)
		}
		entries = append(entries, e)
	}
	b, _ := json.Marshal(map[string]interface{}{"schemaVersion": 1, "entries": entries})
	writeFile(t, catalogState(root), string(b))
}

func TestPackageDigestMatchesMonomind(t *testing.T) {
	root := catalogRoot(t)
	pk := filepath.Join(root, ".monomind", "catalog", "packages")
	for name, want := range map[string]string{"sec": bpSecPackage, "dev": bpDevPackage} {
		got, err := packageDigest(filepath.Join(pk, name, want[:12]))
		if err != nil || got != want {
			t.Errorf("%s: %s (%v), monomind %s", name, got, err, want)
		}
	}
}

// The vector pinned in mono-agent#299, and monomind's own hashes for orgs
// with several, repeated and non-string blueprints.
func TestBlueprintHashMatchesMonomind(t *testing.T) {
	root := catalogRoot(t)
	for org, want := range map[string]string{bpOrgPinned: bpHashPined, bpOrgMany: bpHashMany, bpOrgNonString: bpHashNonStr} {
		if got, err := Hash(root, []byte(org)); err != nil || got != want {
			t.Errorf("%s: %s (%v), want %s", org, got, err, want)
		}
	}
}

// Anything the catalog can't vouch for is "unavailable" in monomind and no
// hash here: never verified, never signed.
func TestUnavailableBlueprintHasNoGoHash(t *testing.T) {
	cases := map[string]func(t *testing.T, root string){
		"no catalog": func(t *testing.T, root string) { _ = os.RemoveAll(filepath.Join(root, ".monomind", "catalog")) },
		"revoked": func(t *testing.T, root string) {
			writeCatalogState(t, root, func(e map[string]interface{}) { e["status"] = "revoked" })
		},
		"disabled": func(t *testing.T, root string) {
			writeCatalogState(t, root, func(e map[string]interface{}) { e["status"] = "disabled" })
		},
		"not for org": func(t *testing.T, root string) {
			writeCatalogState(t, root, func(e map[string]interface{}) { e["targets"] = []string{"jev"} })
		},
		"state not JSON": func(t *testing.T, root string) { writeFile(t, catalogState(root), "{") },
		"state wrong version": func(t *testing.T, root string) {
			writeFile(t, catalogState(root), `{"schemaVersion":2,"entries":[]}`)
		},
		"kind differs from id": func(t *testing.T, root string) {
			writeCatalogState(t, root, func(e map[string]interface{}) { e["kind"] = "skill" })
		},
		"package edited": func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, ".monomind", "catalog", "packages", "sec", bpSecPackage[:12], "blueprint.json"), bpSec+" ")
		},
		"file added to package": func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, ".monomind", "catalog", "packages", "sec", bpSecPackage[:12], "extra"), "x")
		},
		"package missing": func(t *testing.T, root string) {
			_ = os.RemoveAll(filepath.Join(root, ".monomind", "catalog", "packages", "sec"))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root := catalogRoot(t)
			mutate(t, root)
			h, err := Hash(root, []byte(bpOrgPinned))
			if h != "" || !errors.Is(err, errUnknown) {
				t.Fatalf("hash %q, err %v: want errUnknown", h, err)
			}
		})
	}
}

func TestUnlistedBlueprintHasNoGoHash(t *testing.T) {
	root := catalogRoot(t)
	org := `{"name":"fx","roles":[{"id":"a","blueprint":"nope"}]}`
	if _, err := Hash(root, []byte(org)); !errors.Is(err, errUnknown) {
		t.Fatalf("unlisted blueprint: %v", err)
	}
}

func TestBlueprintPackageSymlinksAreUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlinks")
	}
	root := catalogRoot(t)
	pkg := filepath.Join(root, ".monomind", "catalog", "packages", "sec", bpSecPackage[:12])
	if err := os.Symlink(filepath.Join(pkg, "blueprint.json"), filepath.Join(pkg, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Hash(root, []byte(bpOrgPinned)); !errors.Is(err, errUnknown) {
		t.Fatalf("symlink in package: %v", err)
	}

	// A package dir that resolves outside the store.
	root = catalogRoot(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "blueprint.json"), bpSec)
	dir := filepath.Join(root, ".monomind", "catalog", "packages", "sec", bpSecPackage[:12])
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Hash(root, []byte(bpOrgPinned)); !errors.Is(err, errUnknown) {
		t.Fatalf("package outside the store: %v", err)
	}
}

func TestBlueprintOrgVerifiesAndGoesStaleWhenCatalogChanges(t *testing.T) {
	operatorDirForTest(t)
	root := catalogRoot(t)
	writeOrg(t, root, "fx", bpOrgPinned)
	signFixture(t, root, "fx", []byte(bpOrgPinned))
	if st := Verify(root, "fx", []byte(bpOrgPinned)); !st.OK() {
		t.Fatalf("state %+v", st)
	}
	// The blueprint is re-staged with other content (a new package, a new
	// entry digest): what the operator signed no longer holds.
	other := `{"name":"sec","description":"Reviews code","skills":["audit","shell"]}`
	writeFile(t, filepath.Join(root, ".monomind", "catalog", "packages", "sec", "new", "blueprint.json"), other)
	sha, err := packageDigest(filepath.Join(root, ".monomind", "catalog", "packages", "sec", "new"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, ".monomind", "catalog", "packages", "sec", "new"),
		filepath.Join(root, ".monomind", "catalog", "packages", "sec", sha[:12])); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]interface{}{"schemaVersion": 1, "entries": []interface{}{
		map[string]interface{}{"id": "blueprint:sec", "kind": "blueprint", "status": "active", "sha256": sha, "targets": []string{"org"}}}})
	writeFile(t, catalogState(root), string(b))
	if st := Verify(root, "fx", []byte(bpOrgPinned)); st.State != StateChanged {
		t.Fatalf("state %+v, want changed", st)
	}
}

func TestBlueprintOrgWithoutCatalogIsUnknown(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	loaded := writeOrg(t, root, "fx", bpOrgPinned)
	signFixture(t, root, "fx", []byte(signedBody)) // a sidecar exists
	if st := Verify(root, "fx", []byte(bpOrgPinned)); st.State != StateUnknown {
		t.Fatalf("state %+v", st)
	}
	if Before(context.Background(), nil, root, "fx", loaded, false).Eligible() {
		t.Fatal("an unavailable blueprint is eligible for automatic re-signing")
	}
}

// A signed blueprint org is re-signed after mono-agent's own edit, with
// the blueprint digest pinned as verified: a blueprint changed in between
// is not signed.
func TestBlueprintOrgReSignedWithPinnedDigest(t *testing.T) {
	operatorDirForTest(t)
	root := catalogRoot(t)
	loaded := writeOrg(t, root, "fx", bpOrgPinned)
	signFixture(t, root, "fx", []byte(bpOrgPinned))
	ctx := context.Background()

	pre := Before(ctx, nil, root, "fx", loaded, false)
	if !pre.Eligible() {
		t.Fatalf("a signed blueprint org should be eligible: %+v", pre)
	}
	own := `{"name":"fx","goal":"ship it","roles":[{"id":"boss","type":"boss","reports_to":null,"title":"CEO","blueprint":"sec"}],"autonomy":{"level":"low"}}`
	sha := writeOrg(t, root, "fx", own)
	s := &fakeSigner{t: t}
	if out := pre.After(ctx, s, "fx", sha); !out.Signed || s.calls != 1 {
		t.Fatalf("outcome %+v (calls %d)", out, s.calls)
	}

	// Same again, but the blueprint is rewritten after the check.
	loaded = SHA256([]byte(own))
	pre = Before(ctx, nil, root, "fx", loaded, false)
	if !pre.Eligible() {
		t.Fatalf("not eligible: %+v", pre)
	}
	sha = writeOrg(t, root, "fx", bpOrgPinned)
	writeFile(t, filepath.Join(root, ".monomind", "catalog", "packages", "sec", bpSecPackage[:12], "blueprint.json"), `{"name":"sec"}`)
	s = &fakeSigner{t: t}
	if out := pre.After(ctx, s, "fx", sha); out.Signed || s.calls != 0 {
		t.Fatalf("signed a changed blueprint: %+v (calls %d)", out, s.calls)
	}
}

// The review guard stamps the catalog state and the blueprint package's
// files, so a swap of one for monomind's read is seen.
func TestStampCoversBlueprintFiles(t *testing.T) {
	operatorDirForTest(t)
	root := catalogRoot(t)
	writeOrg(t, root, "fx", bpOrgPinned)
	a, err := StampDefinition(root, "fx")
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(realRoot(root), ".monomind", "catalog", "packages", "sec", bpSecPackage[:12], "blueprint.json")
	for _, p := range []string{catalogState(realRoot(root)), pkg} {
		if _, ok := a[p]; !ok {
			t.Errorf("%s is not stamped", p)
		}
	}
	if b, _ := StampDefinition(root, "fx"); !a.Same(b) {
		t.Fatal("an untouched catalog should stamp the same")
	}
	writeFile(t, pkg, bpSec)
	if b, _ := StampDefinition(root, "fx"); a.Same(b) {
		t.Fatal("a rewritten blueprint file should move the stamp")
	}
}

func TestSymlinkedCatalogIsUntrusted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlinks")
	}
	operatorDirForTest(t)
	root := catalogRoot(t)
	writeOrg(t, root, "fx", bpOrgPinned)
	if r := Untrusted(root, "fx"); r != "" {
		t.Skipf("filesystem untrusted here: %s", r)
	}
	store := filepath.Join(root, ".monomind", "catalog", "packages")
	moved := filepath.Join(t.TempDir(), "packages")
	if err := os.Rename(store, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, store); err != nil {
		t.Fatal(err)
	}
	if Untrusted(root, "fx") == "" {
		t.Fatal("a symlinked blueprint package store should be untrusted")
	}
}
