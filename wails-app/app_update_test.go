package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAppRelease serves a release with the given assets and a
// SHA256SUMS.txt generated from them (sumsOverride replaces it; sumsStatus
// makes fetching it fail; noSums leaves it out; missing lists assets
// advertised but answering 404).
type fakeAppRelease struct {
	assets       map[string][]byte
	sumsOverride string
	sumsStatus   int
	noSums       bool
	missing      map[string]bool
}

func (f fakeAppRelease) server(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
			var as []map[string]string
			for n := range f.assets {
				as = append(as, map[string]string{"name": n, "browser_download_url": srv.URL + "/dl/" + n})
			}
			if !f.noSums {
				as = append(as, map[string]string{"name": "SHA256SUMS.txt", "browser_download_url": srv.URL + "/sums"})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.9.9", "assets": as})
		case r.URL.Path == "/sums":
			if f.sumsStatus != 0 {
				http.Error(w, "nope", f.sumsStatus)
				return
			}
			if f.sumsOverride != "" {
				w.Write([]byte(f.sumsOverride))
				return
			}
			for n, b := range f.assets {
				w.Write([]byte(sha256hex(b) + "  " + n + "\n"))
			}
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			n := strings.TrimPrefix(r.URL.Path, "/dl/")
			if f.missing[n] {
				http.NotFound(w, r)
				return
			}
			w.Write(f.assets[n])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func linuxTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, body := range files {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func testUpdater(srv *httptest.Server, goos, goarch, exe string, started *[]string) appUpdater {
	return appUpdater{client: srv.Client(), apiURL: srv.URL + "/latest", goos: goos, goarch: goarch, exe: exe,
		progress: func(string) {},
		startDetached: func(name string, args ...string) error {
			*started = append(*started, name+" "+strings.Join(args, " "))
			return nil
		}}
}

// linuxInstall makes <dir>/MonoAgent-linux-amd64 (+ optional CLI) and
// returns the app path.
func linuxInstall(t *testing.T, cliName string) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "MonoAgent-linux-amd64")
	os.WriteFile(exe, []byte("old app"), 0o755)
	if cliName != "" {
		os.WriteFile(filepath.Join(dir, cliName), []byte("old cli"), 0o755)
	}
	return exe
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// dirNames lists dir's entries (to catch leftover staging/backup files).
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, _ := os.ReadDir(dir)
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestAppUpdateLinuxInstallsAppAndBundledCLI(t *testing.T) {
	tgz := linuxTarball(t, map[string]string{"MonoAgent-linux-amd64": "new app", "monoagentcli": "new cli"})
	srv := fakeAppRelease{assets: map[string][]byte{"MonoAgent-linux-amd64.tar.gz": tgz}}.server(t)
	for _, existing := range []string{"monoagentcli", "monoagentcli-linux-amd64-bundled", ""} {
		t.Run("cli="+existing, func(t *testing.T) {
			exe := linuxInstall(t, existing)
			var started []string
			res := testUpdater(srv, "linux", "amd64", exe, &started).run()
			if !res.Success || res.NewVersion != "v9.9.9" {
				t.Fatalf("result = %+v", res)
			}
			dir := filepath.Dir(exe)
			if readFile(t, exe) != "new app" {
				t.Fatal("app not replaced")
			}
			cli := existing
			if cli == "" {
				cli = "monoagentcli" // the tarball's name when none was there
			}
			if readFile(t, filepath.Join(dir, cli)) != "new cli" {
				t.Fatalf("bundled CLI %s not updated", cli)
			}
			if got := dirNames(t, dir); len(got) != 2 {
				t.Fatalf("leftovers: %v", got)
			}
		})
	}
}

func TestAppUpdateFailsClosed(t *testing.T) {
	good := linuxTarball(t, map[string]string{"MonoAgent-linux-amd64": "new app", "monoagentcli": "new cli"})
	name := "MonoAgent-linux-amd64.tar.gz"
	for _, tc := range []struct {
		desc, want string
		rel        fakeAppRelease
	}{
		{"mismatch", "SHA-256 mismatch", fakeAppRelease{assets: map[string][]byte{name: good},
			sumsOverride: sha256hex([]byte("other")) + "  " + name + "\n"}},
		{"missing entry", "has no entry for " + name, fakeAppRelease{assets: map[string][]byte{name: good},
			sumsOverride: sha256hex(good) + "  " + name + "-old\n"}},
		{"sums fetch fails", "fetch SHA256SUMS.txt", fakeAppRelease{assets: map[string][]byte{name: good}, sumsStatus: 500}},
		{"no sums asset", "has no SHA256SUMS.txt", fakeAppRelease{assets: map[string][]byte{name: good}, noSums: true}},
		{"asset 404", "download error", fakeAppRelease{assets: map[string][]byte{name: good}, missing: map[string]bool{name: true}}},
		{"no app in tarball", "not found in downloaded archive", fakeAppRelease{assets: map[string][]byte{
			name: linuxTarball(t, map[string]string{"../../evil": "x"})}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			exe := linuxInstall(t, "monoagentcli")
			var started []string
			res := testUpdater(tc.rel.server(t), "linux", "amd64", exe, &started).run()
			if res.Success || !strings.Contains(res.Error, tc.want) {
				t.Fatalf("result = %+v, want error containing %q", res, tc.want)
			}
			dir := filepath.Dir(exe)
			if readFile(t, exe) != "old app" || readFile(t, filepath.Join(dir, "monoagentcli")) != "old cli" {
				t.Fatal("installation changed on a failed update")
			}
			if got := dirNames(t, dir); len(got) != 2 {
				t.Fatalf("leftovers: %v", got)
			}
		})
	}
}

func TestAppUpdateUnsupportedPlatform(t *testing.T) {
	var started []string
	u := appUpdater{goos: "linux", goarch: "arm64", progress: func(string) {}, startDetached: func(string, ...string) error {
		started = append(started, "x")
		return nil
	}}
	if res := u.run(); res.Success || !strings.Contains(res.Error, "not supported on linux/arm64") {
		t.Fatalf("result = %+v", res)
	}
}

func TestAppUpdateWindowsSwapsExeAndCLI(t *testing.T) {
	assets := map[string][]byte{
		"MonoAgent-windows-amd64.exe":            []byte("new app"),
		"monoagentcli-windows-amd64-bundled.exe": []byte("new cli"),
	}
	srv := fakeAppRelease{assets: assets}.server(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "MonoAgent-windows-amd64.exe")
	cli := filepath.Join(dir, "monoagentcli-windows-amd64-bundled.exe")
	os.WriteFile(exe, []byte("old app"), 0o755)
	os.WriteFile(cli, []byte("old cli"), 0o755)
	var started []string
	res := testUpdater(srv, "windows", "amd64", exe, &started).run()
	if !res.Success {
		t.Fatalf("result = %+v", res)
	}
	if readFile(t, exe) != "new app" || readFile(t, cli) != "new cli" {
		t.Fatal("exe or CLI not replaced")
	}
	// The running exe cannot be deleted on Windows: backups stay until the
	// restart script removes them.
	if readFile(t, exe+".bak") != "old app" {
		t.Fatal("no backup of the old exe")
	}
	bat := readFile(t, filepath.Join(dir, ".monoagent-update.bat"))
	if !strings.Contains(bat, `del /Q "`+exe+`.bak"`) || !strings.Contains(bat, `start "" "`+exe+`"`) {
		t.Fatalf("bat:\n%s", bat)
	}
	if len(started) != 1 || !strings.HasPrefix(started[0], "cmd.exe /C ") {
		t.Fatalf("restart = %v", started)
	}

	// A mismatching CLI asset aborts the whole update, the app included.
	bad := fakeAppRelease{assets: assets, sumsOverride: sha256hex(assets["MonoAgent-windows-amd64.exe"]) +
		"  MonoAgent-windows-amd64.exe\n" + sha256hex([]byte("x")) + "  monoagentcli-windows-amd64-bundled.exe\n"}
	dir2 := t.TempDir()
	exe2 := filepath.Join(dir2, "MonoAgent.exe")
	os.WriteFile(exe2, []byte("old app"), 0o755)
	os.WriteFile(filepath.Join(dir2, "monoagentcli.exe"), []byte("old cli"), 0o755)
	res = testUpdater(bad.server(t), "windows", "amd64", exe2, &started).run()
	if res.Success || readFile(t, exe2) != "old app" || len(dirNames(t, dir2)) != 2 {
		t.Fatalf("tampered CLI: %+v %v", res, dirNames(t, dir2))
	}
}

func TestAppUpdateMacOSSwapsBundle(t *testing.T) {
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for n, body := range map[string]string{
		"MonoAgent.app/Contents/MacOS/monoagent-ui": "new app",
		"MonoAgent.app/Contents/MacOS/monoagentcli": "new cli",
	} {
		w, _ := zw.Create(n)
		w.Write([]byte(body))
	}
	zw.Close()
	srv := fakeAppRelease{assets: map[string][]byte{"MonoAgent-darwin-arm64.zip": zbuf.Bytes()}}.server(t)
	apps := t.TempDir()
	macos := filepath.Join(apps, "MonoAgent.app", "Contents", "MacOS")
	os.MkdirAll(macos, 0o755)
	exe := filepath.Join(macos, "monoagent-ui")
	os.WriteFile(exe, []byte("old app"), 0o755)
	os.WriteFile(filepath.Join(macos, "monoagentcli"), []byte("old cli"), 0o755)
	var started []string
	res := testUpdater(srv, "darwin", "arm64", exe, &started).run()
	if !res.Success {
		t.Fatalf("result = %+v", res)
	}
	if readFile(t, exe) != "new app" || readFile(t, filepath.Join(macos, "monoagentcli")) != "new cli" {
		t.Fatal("bundle not replaced")
	}
	if got := dirNames(t, apps); len(got) != 1 {
		t.Fatalf("leftovers next to the bundle: %v", got)
	}
	if len(started) != 1 || !strings.Contains(started[0], "open '"+filepath.Join(apps, "MonoAgent.app")+"'") {
		t.Fatalf("restart = %v", started)
	}
}

func TestSwapAllRollsBack(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(a, []byte("old a"), 0o644)
	os.WriteFile(b, []byte("old b"), 0o644)
	sa, _ := stageBytes(dir, []byte("new a"))
	err := swapAll([]swapItem{{target: a, staged: sa}, {target: b, staged: filepath.Join(dir, "missing")}})
	if err == nil {
		t.Fatal("swap with a missing staged file succeeded")
	}
	if readFile(t, a) != "old a" || readFile(t, b) != "old b" || len(dirNames(t, dir)) != 2 {
		t.Fatalf("not rolled back: %v", dirNames(t, dir))
	}
}
