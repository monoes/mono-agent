package orgsign

import (
	"os"
	"testing"
)

// A process inside an org role (monomind's own markers) never reads or
// touches the operator dir: monomind hides it from roles by design, and
// the Go check would otherwise read the signing key and the signatures.
func TestRoleContextNeverReadsOperatorDir(t *testing.T) {
	for _, m := range AgentContextMarkers() {
		t.Setenv(m, "")
	}
	operatorDirForTest(t)
	root := t.TempDir()
	raw := []byte(`{"name":"acme","goal":"g","roles":[{"id":"ceo","title":"CEO","type":"coordinator","reports_to":null}]}`)
	writeOrg(t, root, "acme", string(raw))
	signFixture(t, root, "acme", raw)
	if st := Verify(root, "acme", raw); !st.OK() {
		t.Fatalf("operator's own check = %+v", st)
	}

	for _, marker := range roleContextMarkers {
		t.Run(marker, func(t *testing.T) {
			t.Setenv(marker, "ceo")
			if st := Verify(root, "acme", raw); st.State != StateUnknown {
				t.Errorf("Verify under %s = %+v, want unknown (no read of the operator dir)", marker, st)
			}
			if h, ok := SignedHash(root, "acme"); ok || h != "" {
				t.Errorf("SignedHash under %s read the signature: %q", marker, h)
			}
			if err := Withdraw(root, "acme"); err == nil {
				t.Errorf("Withdraw under %s did not refuse", marker)
			}
			path, _ := SignaturePath(root, "acme")
			if _, err := os.Stat(path); err != nil {
				t.Errorf("signature touched under %s: %v", marker, err)
			}
		})
	}
}
