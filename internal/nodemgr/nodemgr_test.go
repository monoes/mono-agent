package nodemgr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type tarEntry struct {
	name, body, link string
	dir              bool
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o755}
		switch {
		case e.dir:
			hdr.Typeflag = tar.TypeDir
		case e.link != "":
			hdr.Typeflag, hdr.Linkname = tar.TypeSymlink, e.link
		default:
			hdr.Typeflag, hdr.Size = tar.TypeReg, int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// fakeDist serves index.json, SHASUMS256.txt and one archive per version.
func fakeDist(t *testing.T, m *Manager, archives map[string][]byte, corrupt bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		key := m.indexFileKey()
		fmt.Fprintf(w, `[
 {"version":"v26.1.0","lts":false,"files":[%q]},
 {"version":"v24.2.0","lts":"Krypton","files":["some-other-platform"]},
 {"version":"v24.1.0","lts":"Krypton","files":[%q]},
 {"version":"v22.11.0","lts":"Jod","files":[%q]}
]`, key, key, key)
	})
	for v, data := range archives {
		name := m.archiveName(v)
		sum := sha256.Sum256(data)
		if corrupt {
			sum[0] ^= 0xff
		}
		data := data
		mux.HandleFunc("/v"+v+"/SHASUMS256.txt", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		})
		mux.HandleFunc("/v"+v+"/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(data) })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testManager(t *testing.T) *Manager {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell-script stand-in for node")
	}
	m := New()
	dir := t.TempDir()
	m.Root, m.NpmRoot = filepath.Join(dir, "node"), filepath.Join(dir, "npm-global")
	return m
}

func nodeArchive(t *testing.T, m *Manager, version string) []byte {
	top := strings.TrimSuffix(m.archiveName(version), ".tar.gz") + "/"
	return makeTarGz(t, []tarEntry{
		{name: top, dir: true},
		{name: top + "bin/node", body: "#!/bin/sh\necho v" + version + "\n"},
		{name: top + "lib/node_modules/npm/bin/npm-cli.js", body: "// npm"},
		{name: top + "bin/npm", link: "../lib/node_modules/npm/bin/npm-cli.js"},
	})
}

func TestResolveVersion(t *testing.T) {
	m := testManager(t)
	m.BaseURL = fakeDist(t, m, nil, false).URL
	ctx := context.Background()
	for want, exp := range map[string]string{"": "24.1.0", "lts": "24.1.0", "26": "26.1.0", "v24.9.9": "24.9.9"} {
		got, err := m.ResolveVersion(ctx, want)
		if err != nil || got != exp {
			t.Errorf("ResolveVersion(%q) = %q, %v; want %q", want, got, err, exp)
		}
	}
	if _, err := m.ResolveVersion(ctx, "22"); err == nil {
		t.Error("22.x only has releases below MinVersion; want an error")
	}
}

func TestInstallUseRemove(t *testing.T) {
	m := testManager(t)
	m.BaseURL = fakeDist(t, m, map[string][]byte{"24.1.0": nodeArchive(t, m, "24.1.0")}, false).URL
	ctx := context.Background()

	var lines []string
	v, err := m.Install(ctx, "lts", func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatalf("Install: %v\n%s", err, strings.Join(lines, "\n"))
	}
	if cur, ok := m.Current(); v != "24.1.0" || !ok || cur != v {
		t.Fatalf("installed %q, current %q/%v", v, cur, ok)
	}
	if got, _ := NodeVersion(ctx, m.NodePath(v)); got != "24.1.0" {
		t.Fatalf("node --version = %q", got)
	}
	if link, err := os.Readlink(m.NpmPath(v)); err != nil || link != "../lib/node_modules/npm/bin/npm-cli.js" {
		t.Fatalf("npm symlink: %q, %v", link, err)
	}
	if inst, _ := m.Installed(); len(inst) != 1 || inst[0] != v {
		t.Fatalf("Installed = %v", inst)
	}
	// Staging and download temp files are gone.
	entries, _ := os.ReadDir(m.Root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("leftover %s", e.Name())
		}
	}

	// Installing again only re-activates.
	if _, err := m.Install(ctx, "24.1.0", nil); err != nil {
		t.Fatal(err)
	}

	env := strings.Join(m.NpmEnv(v), "\n")
	if !strings.Contains(env, "NPM_CONFIG_PREFIX="+m.NpmRoot) || !strings.Contains(env, "PATH="+m.BinDir(v)) {
		t.Errorf("NpmEnv missing prefix/PATH:\n%s", env)
	}

	if err := m.Remove(""); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Current(); ok {
		t.Error("still current after Remove")
	}
}

func TestInstallRejectsBadChecksum(t *testing.T) {
	m := testManager(t)
	m.BaseURL = fakeDist(t, m, map[string][]byte{"24.1.0": nodeArchive(t, m, "24.1.0")}, true).URL
	_, err := m.Install(context.Background(), "24.1.0", nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	if inst, _ := m.Installed(); len(inst) != 0 {
		t.Fatalf("nothing should be installed: %v", inst)
	}
}

func TestInstallRejectsEscapingArchive(t *testing.T) {
	m := testManager(t)
	for _, entries := range [][]tarEntry{
		{{name: "../evil", body: "x"}},
		{{name: "node-v24.1.0/bin/npm", link: "../../../../etc/passwd"}},
		{{name: "node-v24.1.0/bin/npm", link: "/etc/passwd"}},
	} {
		arch := makeTarGz(t, entries)
		path := filepath.Join(t.TempDir(), "a.tar.gz")
		os.WriteFile(path, arch, 0o644)
		if err := untarGz(path, t.TempDir()); err == nil {
			t.Errorf("entries %+v: want rejection", entries)
		}
	}
	_ = m
}

func TestCompareAndSuitable(t *testing.T) {
	if Compare("v22.12.0", "22.9.0") != 1 || Compare("22.12.0", "22.12.0") != 0 || Compare("junk", "1.0.0") != -1 {
		t.Error("Compare")
	}
	if Suitable("22.11.9") || !Suitable("v22.12.0") || !Suitable("26.0.0") {
		t.Error("Suitable")
	}
}

func TestActivatePrefersSuitableSystemNode(t *testing.T) {
	m := testManager(t)
	m.BaseURL = fakeDist(t, m, map[string][]byte{"24.1.0": nodeArchive(t, m, "24.1.0")}, false).URL
	home := t.TempDir()
	t.Setenv("HOME", home)
	m.Root, m.NpmRoot = filepath.Join(home, ".monoagent", "node"), filepath.Join(home, ".monoagent", "npm-global")
	if _, err := m.Install(context.Background(), "24.1.0", nil); err != nil {
		t.Fatal(err)
	}

	// Old system node: managed goes first.
	sys := t.TempDir()
	os.WriteFile(filepath.Join(sys, "node"), []byte("#!/bin/sh\necho v20.0.0\n"), 0o755)
	t.Setenv("PATH", sys+string(os.PathListSeparator)+"/bin:/usr/bin")
	Activate(context.Background())
	if first := filepath.SplitList(os.Getenv("PATH"))[0]; first != m.BinDir("24.1.0") {
		t.Fatalf("old system node: PATH starts with %s", first)
	}

	// Suitable system node: it stays first, managed is appended.
	os.WriteFile(filepath.Join(sys, "node"), []byte("#!/bin/sh\necho v24.5.0\n"), 0o755)
	t.Setenv("PATH", sys+string(os.PathListSeparator)+"/bin:/usr/bin")
	Activate(context.Background())
	parts := filepath.SplitList(os.Getenv("PATH"))
	if parts[0] != sys || parts[len(parts)-2] != m.BinDir("24.1.0") {
		t.Fatalf("suitable system node: PATH = %v", parts)
	}
}
