package automation

import (
	"errors"
	"strings"
	"testing"
)

// A package may not take a built-in node namespace as its id: its actions
// would register as "<id>.<action>" beside (or instead of) built-in nodes,
// e.g. "trigger.send_dm" passing as a trigger or "image.resize" colliding
// with the built-in node at startup.
func TestInstallRejectsReservedID(t *testing.T) {
	for _, id := range []string{"trigger", "image", "core", "comm"} {
		t.Run(id, func(t *testing.T) {
			r := newReg(t)
			files := acmeFiles()
			files["automation.json"] = strings.Replace(files["automation.json"], `"id": "acme-crm"`, `"id": "`+id+`"`, 1)
			res, err := r.Install(writeTree(t, t.TempDir(), files), InstallOptions{})
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "built-in node namespace") {
				t.Fatalf("install %q: err = %v", id, err)
			}
			if res == nil || res.Installed {
				t.Fatalf("result: %+v", res)
			}
		})
	}
	if ReservedID("acme-crm") || ReservedID("linkedin") || !ReservedID("trigger") {
		t.Fatal("ReservedID wrong")
	}
}

// A package installed before ids were reserved is unavailable, so it never
// loads or registers nodes.
func TestReservedIDPackageIsUnavailable(t *testing.T) {
	ok, reason := availability(Manifest{ID: "image"})
	if ok || !strings.Contains(reason, "built-in node namespace") {
		t.Fatalf("availability = %v, %q", ok, reason)
	}
}
