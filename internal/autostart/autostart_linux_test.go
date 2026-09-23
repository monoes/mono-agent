//go:build linux

package autostart

import (
	"strings"
	"testing"
)

// The binary path is quoted in ExecStart, so a folder with a space, a % or
// a $ in its name still names one file to systemd.
func TestRenderUnitQuotesTheBinaryPath(t *testing.T) {
	unit, err := renderUnit(`/home/a b/100%/$x/monoagentcli`)
	if err != nil {
		t.Fatal(err)
	}
	want := `ExecStart="/home/a b/100%%/$$x/monoagentcli" daemon`
	if !strings.Contains(unit, want+"\n") {
		t.Fatalf("unit has no %q:\n%s", want, unit)
	}
	if !strings.Contains(unit, "WantedBy=default.target") {
		t.Fatalf("unit is not enabled for the user session:\n%s", unit)
	}
}

func TestSystemdQuoteEscapesQuotesAndBackslashes(t *testing.T) {
	if got, want := systemdQuote(`/a"b\c`), `"/a\"b\\c"`; got != want {
		t.Fatalf("systemdQuote = %s, want %s", got, want)
	}
}
