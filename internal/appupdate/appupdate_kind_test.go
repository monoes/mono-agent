package appupdate

import (
	"testing"

	"github.com/monoes/mono-agent/internal/release"
)

func TestSignedWindowsBundledExeIsKindApp(t *testing.T) {
	m := &release.Manifest{Assets: []release.Asset{
		{OS: "windows", Arch: "amd64", Name: "MonoAgent-windows-amd64.exe", Kind: "app"},
		{OS: "windows", Arch: "amd64", Name: bundledCLIAssetWindows, Kind: "app"},
	}}
	for _, n := range []string{"MonoAgent-windows-amd64.exe", bundledCLIAssetWindows} {
		if _, err := m.Asset(n, "windows", "amd64", "app"); err != nil {
			t.Fatal(err)
		}
	}
	// A manifest that labels the bundled exe "cli" is not accepted for it.
	m.Assets[1].Kind = "cli"
	u := Updater{GOOS: "windows", GOARCH: "amd64"}
	if _, err := u.downloadSigned(&release.Client{}, m, []string{bundledCLIAssetWindows}); err == nil {
		t.Fatal("bundled exe must be selected by kind \"app\"")
	}
}
