package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// fakeAppRelease serves a latest release with the linux app tarball and a
// SHA256SUMS.txt; tamper serves a different archive than the one summed.
func fakeAppRelease(t *testing.T, archive []byte, tamper bool) {
	t.Helper()
	sum := sha256.Sum256(archive)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":"MonoAgent-linux-amd64.tar.gz","browser_download_url":"%s/app"},{"name":"SHA256SUMS.txt","browser_download_url":"%s/sums"}]}`, srv.URL, srv.URL)
		case "/app":
			if tamper {
				_, _ = w.Write(append([]byte("x"), archive...))
				return
			}
			_, _ = w.Write(archive)
		case "/sums":
			fmt.Fprintf(w, "%s  MonoAgent-linux-amd64.tar.gz\n", hex.EncodeToString(sum[:]))
		}
	}))
	t.Cleanup(srv.Close)
	old := latestReleaseURL
	latestReleaseURL = srv.URL + "/latest"
	t.Cleanup(func() { latestReleaseURL = old })
}

func runUpdateAppCmd(t *testing.T, args ...string) (appUpdateResult, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := newUpdateCmd(&globalConfig{JSONOutput: true})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("update: %v (%s)", err, errOut.String())
	}
	var res appUpdateResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	return res, errOut.String()
}

func appInstallDir(t *testing.T) (app, cli string) {
	t.Helper()
	dir := t.TempDir()
	app, cli = filepath.Join(dir, "MonoAgent-linux-amd64"), filepath.Join(dir, "monoagentcli")
	for _, p := range []string{app, cli} {
		if err := os.WriteFile(p, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return app, cli
}

func TestUpdateAppLinuxReplacesAppAndBundledCLI(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux install path")
	}
	fakeAppRelease(t, tgz(t, map[string]string{"MonoAgent-linux-amd64": "new-app", "monoagentcli": "new-cli"}), false)
	app, cli := appInstallDir(t)
	res, progress := runUpdateAppCmd(t, "--app", app, "--current", "v1.0.0")
	if !res.Success || res.NewVersion != "v9.9.9" || res.Restart != "quit" || res.Error != "" {
		t.Fatalf("res = %+v", res)
	}
	for p, want := range map[string]string{app: "new-app", cli: "new-cli"} {
		if b, _ := os.ReadFile(p); string(b) != want {
			t.Fatalf("%s = %q, want %q", p, b, want)
		}
	}
	if !strings.Contains(progress, `"kind":"line"`) || !strings.Contains(progress, "Checksum verified") {
		t.Fatalf("progress = %s", progress)
	}
}

func TestUpdateAppRefusesATamperedDownload(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux install path")
	}
	fakeAppRelease(t, tgz(t, map[string]string{"MonoAgent-linux-amd64": "evil"}), true)
	app, cli := appInstallDir(t)
	res, _ := runUpdateAppCmd(t, "--app", app, "--current", "v1.0.0")
	if res.Success || res.Error == "" {
		t.Fatalf("a download that doesn't match SHA256SUMS must fail: %+v", res)
	}
	for _, p := range []string{app, cli} {
		if b, _ := os.ReadFile(p); string(b) != "old" {
			t.Fatalf("%s changed to %q after a failed verification", p, b)
		}
	}
}

func TestUpdateAppUpToDate(t *testing.T) {
	fakeAppRelease(t, tgz(t, map[string]string{"MonoAgent-linux-amd64": "x"}), false)
	app, _ := appInstallDir(t)
	res, _ := runUpdateAppCmd(t, "--app", app, "--current", "v9.9.9")
	if !res.Success || !res.UpToDate {
		t.Fatalf("res = %+v", res)
	}
	if b, _ := os.ReadFile(app); string(b) != "old" {
		t.Fatal("up to date must not install")
	}
}

// Every path in the swap scripts is quoted so a path with $, backticks or
// spaces can neither expand nor split.
func TestSwapScriptsQuotePaths(t *testing.T) {
	bundle := "/Apps/Mono $(x) Agent.app"
	s := darwinSwapScript(bundle, "/tmp/x/MonoAgent.app", "/tmp/x")
	if strings.Count(s, "'"+bundle+"'") != 3 {
		t.Fatalf("darwin script must single-quote the bundle each time:\n%s", s)
	}
	w := windowsSwapScript(`C:\A B\app.exe.new`, `C:\A B\app.exe`, `C:\t\u.bat`)
	if !strings.Contains(w, `move /Y "C:\A B\app.exe.new" "C:\A B\app.exe"`) || !strings.HasSuffix(w, "\r\n") {
		t.Fatalf("windows script = %q", w)
	}
}

func TestUpdateCheckAndAppAreExclusive(t *testing.T) {
	cmd := newUpdateCmd(&globalConfig{JSONOutput: true})
	cmd.SetArgs([]string{"--check", "--app", "/x"})
	if err := cmd.Execute(); exitCode(err) != 3 {
		t.Fatalf("exit = %d, want 3", exitCode(err))
	}
}
