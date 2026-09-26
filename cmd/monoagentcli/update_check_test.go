package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewerRelease(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.73.0", "v0.72.0", true},
		{"v0.10.0", "v0.9.9", true},
		{"v0.72.0", "v0.72.0", false},
		{"v0.71.0", "v0.72.0", false}, // local is newer: not an "update"
		{"v1.0.0", "0.99.1", true},
		{"v0.73.0", "dev", false},
		{"v0.73.0", "v0.72.0-4-gabc1234", false},
		{"", "v0.72.0", false},
	} {
		if got := newerRelease(c.latest, c.current); got != c.want {
			t.Errorf("newerRelease(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func runUpdateCheckCmd(t *testing.T, args ...string) (updateCheck, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newUpdateCmd(&globalConfig{JSONOutput: true})
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	var res updateCheck
	if err == nil {
		if jerr := json.Unmarshal(out.Bytes(), &res); jerr != nil {
			t.Fatalf("not JSON: %v\n%s", jerr, out.String())
		}
	}
	return res, err
}

func TestUpdateCheckReportsWithoutDownloading(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"tag_name":"v0.73.0","html_url":"https://example.test/r/v0.73.0","assets":[{"name":"x","browser_download_url":"https://example.test/x"}]}`))
	}))
	defer srv.Close()
	old := latestReleaseURL
	latestReleaseURL = srv.URL + "/latest"
	t.Cleanup(func() { latestReleaseURL = old })

	res, err := runUpdateCheckCmd(t, "--check", "--current", "v0.72.0")
	if err != nil {
		t.Fatal(err)
	}
	if !res.UpdateAvailable || res.LatestVersion != "v0.73.0" || res.CurrentVersion != "v0.72.0" || res.ReleaseURL != "https://example.test/r/v0.73.0" {
		t.Fatalf("res = %+v", res)
	}
	if len(paths) != 1 {
		t.Fatalf("--check must only read the release, got requests %v", paths)
	}
	if res, _ := runUpdateCheckCmd(t, "--check", "--current", "v0.73.0"); res.UpdateAvailable {
		t.Fatalf("same version reported as an update: %+v", res)
	}
}

func TestUpdateCheckNetworkFailureIsAResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	old := latestReleaseURL
	latestReleaseURL = srv.URL
	t.Cleanup(func() { latestReleaseURL = old })
	res, err := runUpdateCheckCmd(t, "--check", "--current", "v0.72.0")
	if err != nil || res.Error == "" || res.UpdateAvailable {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestUpdateCurrentNeedsCheck(t *testing.T) {
	if _, err := runUpdateCheckCmd(t, "--current", "v1"); exitCode(err) != 3 {
		t.Fatalf("exit = %d, want 3", exitCode(err))
	}
}
