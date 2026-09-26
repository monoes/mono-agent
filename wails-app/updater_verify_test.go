package main

import (
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

const testAsset = "monoagentcli-linux-amd64"

// fakeRelease serves a latest-release payload, the binary and SHA256SUMS.txt.
// sums == "" leaves the manifest out of the release; sumsStatus != 200 makes
// fetching it fail.
type fakeRelease struct {
	binary     []byte
	sums       string
	sumsStatus int
}

func (f fakeRelease) server(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			assets := []map[string]string{{"name": testAsset, "browser_download_url": srv.URL + "/bin"}}
			if f.sums != "" || f.sumsStatus != 0 {
				assets = append(assets, map[string]string{"name": "SHA256SUMS.txt", "browser_download_url": srv.URL + "/sums"})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.9.9", "assets": assets})
		case "/bin":
			w.Write(f.binary)
		case "/sums":
			if f.sumsStatus != 0 && f.sumsStatus != 200 {
				http.Error(w, "gone", f.sumsStatus)
				return
			}
			w.Write([]byte(f.sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sumLine(data []byte, name string) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:]) + "  " + name + "\n"
}

// installedCLI makes a fake installed CLI and returns its path.
func installedCLI(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "monoagentcli")
	if err := os.WriteFile(p, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// assertUntouched: the old CLI is still in place and no temp/backup is left.
func assertUntouched(t *testing.T, cliPath string) {
	t.Helper()
	b, err := os.ReadFile(cliPath)
	if err != nil || string(b) != "old binary" {
		t.Fatalf("CLI changed on a failed update: %q, %v", b, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(cliPath))
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("leftover files: %v", names)
	}
}

func TestSelfUpdateVerifiesChecksum(t *testing.T) {
	newBin := []byte("new binary v9.9.9")
	other := []byte("something else")
	for _, tc := range []struct {
		name    string
		rel     fakeRelease
		wantErr string // "" = success
	}{
		{"good checksum", fakeRelease{binary: newBin, sums: sumLine([]byte("x"), "other-file") + sumLine(newBin, testAsset)}, ""},
		{"binary-mode entry", fakeRelease{binary: newBin, sums: strings.Replace(sumLine(newBin, testAsset), "  ", " *", 1)}, ""},
		{"mismatch", fakeRelease{binary: newBin, sums: sumLine(other, testAsset)}, "SHA-256 mismatch"},
		{"missing entry", fakeRelease{binary: newBin, sums: sumLine(newBin, "monoagentcli-darwin-arm64")}, "has no entry for " + testAsset},
		{"prefix is not an exact entry", fakeRelease{binary: newBin, sums: sumLine(newBin, testAsset+"-bundled")}, "has no entry"},
		{"sums fetch fails", fakeRelease{binary: newBin, sumsStatus: 500}, "fetch SHA256SUMS.txt"},
		{"no sums asset", fakeRelease{binary: newBin}, "has no SHA256SUMS.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := tc.rel.server(t)
			cli := installedCLI(t)
			var steps []string
			res := selfUpdate(srv.Client(), srv.URL+"/latest", testAsset, cli, func(m string) { steps = append(steps, m) })
			if tc.wantErr == "" {
				if !res.Success || res.NewVersion != "v9.9.9" || res.Error != "" {
					t.Fatalf("result = %+v", res)
				}
				if b, _ := os.ReadFile(cli); string(b) != string(newBin) {
					t.Fatalf("installed %q", b)
				}
				if steps[len(steps)-1] != "Update complete!" {
					t.Fatalf("progress = %v", steps)
				}
				return
			}
			if res.Success || !strings.Contains(res.Error, tc.wantErr) {
				t.Fatalf("result = %+v, want error containing %q", res, tc.wantErr)
			}
			assertUntouched(t, cli)
		})
	}
}

func TestSelfUpdateRefusesErrorPageAsBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1", "assets": []map[string]string{
				{"name": testAsset, "browser_download_url": "http://" + r.Host + "/bin"},
				{"name": "SHA256SUMS.txt", "browser_download_url": "http://" + r.Host + "/sums"},
			}})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	cli := installedCLI(t)
	res := selfUpdate(srv.Client(), srv.URL+"/latest", testAsset, cli, func(string) {})
	if res.Success || !strings.Contains(res.Error, "download error") {
		t.Fatalf("result = %+v", res)
	}
	assertUntouched(t, cli)
}
