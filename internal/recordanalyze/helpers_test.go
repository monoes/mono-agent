package recordanalyze

import (
	"context"
	"encoding/json"
	actionpkg "github.com/monoes/mono-agent/internal/action"
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T, name string) *Recording {
	t.Helper()
	rec, err := LoadRecording(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return rec
}

func analyzeFixture(t *testing.T, name string) *Analysis {
	t.Helper()
	rec := loadFixture(t, name)
	return Detect(Normalize(rec.Summary, rec.Events))
}

func answer(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "answers", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stubRunner returns canned answers in order and records the prompts.
type stubRunner struct {
	answers []string
	prompts []string
}

func (s *stubRunner) Run(_ context.Context, prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	if len(s.prompts) > len(s.answers) {
		return s.answers[len(s.answers)-1], nil
	}
	return s.answers[len(s.prompts)-1], nil
}

func kinds_(steps []Step) []string {
	var out []string
	for _, s := range steps {
		out = append(out, s.Kind)
	}
	return out
}

type (
	actionFragment    = actionpkg.FragmentDef
	actionFragmentDef = actionpkg.FragmentDef
	actionStep        = actionpkg.StepDef
)

type jsonRaw = json.RawMessage
