package release

import (
	"os"
	"strings"
	"testing"
)

// The release workflow now holds the signing key as an environment secret. These checks pin the
// properties that keep that safe, so a later edit cannot quietly widen where the key goes.
func TestReleaseWorkflowSigningKeyHandling(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	wf := string(raw)

	if n := strings.Count(wf, "secrets.RELEASE_SIGNING_KEY"); n != 1 {
		t.Fatalf("secrets.RELEASE_SIGNING_KEY referenced %d times, want exactly 1 (the signing step's env)", n)
	}
	for _, bad := range []string{"set -x", "set -o xtrace", "printenv", "toJSON(secrets", "toJson(secrets"} {
		if strings.Contains(wf, bad) {
			t.Errorf("workflow contains %q", bad)
		}
	}

	start := strings.Index(wf, "\n  sign-manifest:\n")
	end := strings.Index(wf, "\n  publish-releases-repo:\n")
	if start < 0 || end < start {
		t.Fatal("sign-manifest / publish-releases-repo jobs not found")
	}
	job := wf[start:end]
	for _, want := range []string{
		"if: github.event_name == 'push' && github.ref == 'refs/heads/master'",
		"environment: release",
		"needs: [version, release-manifest, release]",
		"permissions:\n      contents: read\n",
		"persist-credentials: false",
		"-key-env RELEASE_SIGNING_KEY",
		"-assets-dir bundle",
		"-expect-version \"${TAG}\"",
		"-min-version",
		"release verify",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("sign-manifest lacks %q", want)
		}
	}
	// The key is set in the env of one step, never at job level.
	jobEnv := job[strings.Index(job, "    env:\n"):strings.Index(job, "    steps:\n")]
	if strings.Contains(jobEnv, "RELEASE_SIGNING_KEY") {
		t.Error("RELEASE_SIGNING_KEY is set at job level")
	}
	if !strings.Contains(job, "        env:\n          RELEASE_SIGNING_KEY: ${{ secrets.RELEASE_SIGNING_KEY }}\n") {
		t.Error("RELEASE_SIGNING_KEY is not in a step-level env")
	}
	// Only the publish job gets the releases-repo token, and only in its upload step.
	pub := wf[end:]
	if strings.Count(wf, "secrets.RELEASES_REPO_TOKEN") != 1 || !strings.Contains(pub, "GH_TOKEN: ${{ secrets.RELEASES_REPO_TOKEN }}") {
		t.Error("RELEASES_REPO_TOKEN must appear once, in the publish upload step")
	}
	if !strings.Contains(pub, "needs: [version, release-manifest, release, sign-manifest]") {
		t.Error("publish-releases-repo must need sign-manifest")
	}
	if !strings.Contains(pub, "already has manifest.json.sig") {
		t.Error("the never-overwrite-a-signed-manifest protection is gone")
	}
	if strings.Contains(pub, "artifacts/unsigned-manifest/manifest.json ") {
		t.Error("publish must not upload the unsigned manifest.json")
	}
}
