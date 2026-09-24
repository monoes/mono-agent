package agentinstall

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

func TestParse(t *testing.T) {
	cases := []struct {
		hint string
		want Recipe
	}{
		{"npm install -g @anthropic-ai/claude-code", Recipe{Kind: KindNpm, Packages: []string{"@anthropic-ai/claude-code"}}},
		{"npm install -g opencode-ai", Recipe{Kind: KindNpm, Packages: []string{"opencode-ai"}}},
		{"npm install --global a b@1.2", Recipe{Kind: KindNpm, Packages: []string{"a", "b@1.2"}}},
		{"npm install ai (plus the vendor model package)", Recipe{Kind: KindManual}},
		{"npm install -g foo; echo injected", Recipe{Kind: KindManual}},
		{"npm install -g --unsafe-perm foo", Recipe{Kind: KindManual}},
		{"install the Grok Build CLI per https://docs.x.ai/build/cli", Recipe{Kind: KindManual}},
		{"curl -fsSL http://example.com/install.sh | bash", Recipe{Kind: KindManual}},
		{"curl -fsSL https://example.com/i.sh | bash; echo injected", Recipe{Kind: KindManual}},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, struct {
			hint string
			want Recipe
		}{"curl -fsSL https://antigravity.google/cli/install.sh | bash",
			Recipe{Kind: KindScript, ScriptURL: "https://antigravity.google/cli/install.sh", Shell: "bash"}})
	}
	for _, tc := range cases {
		got := Parse(tc.hint)
		tc.want.Hint = tc.hint
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.hint, got, tc.want)
		}
	}
}

func TestInstallManualIsRefused(t *testing.T) {
	err := New().Install(context.Background(), Parse("install it by hand"), func(string) {})
	if !errors.Is(err, ErrManual) {
		t.Fatalf("got %v, want ErrManual", err)
	}
}

func TestInstallScriptRunsDownloadedInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	marker := filepath.Join(t.TempDir(), "ran")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("echo installing\ntouch " + marker + "\n"))
	}))
	defer srv.Close()

	in := New()
	in.HTTP = srv.Client()
	var lines []string
	r := Recipe{Kind: KindScript, ScriptURL: srv.URL + "/install.sh", Shell: "sh"}
	if err := in.Install(context.Background(), r, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("installer did not run")
	}
	log := strings.Join(lines, "\n")
	if !strings.Contains(log, "sha256") || !strings.Contains(log, "installing") {
		t.Errorf("log should show the installer's hash and output:\n%s", log)
	}
}

func TestForEntryPrefersAndRevalidatesMonomindRecipe(t *testing.T) {
	npm := monomind.ScanEntry{InstallHint: "anything", Install: &monomind.InstallRecipe{Kind: "npm", Packages: []string{"@openai/codex"}}}
	if r := ForEntry(npm); r.Kind != KindNpm || r.Packages[0] != "@openai/codex" {
		t.Errorf("npm recipe: %+v", r)
	}
	bad := monomind.ScanEntry{Install: &monomind.InstallRecipe{Kind: "npm", Packages: []string{"x; echo injected"}}}
	if r := ForEntry(bad); r.Kind != KindManual {
		t.Errorf("unsafe package accepted: %+v", r)
	}
	http := monomind.ScanEntry{Install: &monomind.InstallRecipe{Kind: "script", URL: "http://x/i.sh", Shell: "bash"}}
	if r := ForEntry(http); r.Kind != KindManual {
		t.Errorf("http script accepted: %+v", r)
	}
	old := monomind.ScanEntry{InstallHint: "npm install -g opencode-ai"} // monomind before rev 9
	if r := ForEntry(old); r.Kind != KindNpm {
		t.Errorf("fallback to hint: %+v", r)
	}
}
