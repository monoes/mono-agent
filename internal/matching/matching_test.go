// internal/matching/matching_test.go
package matching

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/monomind"
)

func testJob() *applications.Application {
	return &applications.Application{
		ID: "app-1", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", Description: "Build APIs in Go."},
	}
}

func TestBuildPromptIncludesJobAndExcerpts(t *testing.T) {
	excerpts := []monomind.KnowledgeResult{
		{Path: "/vault/resume.txt", Excerpt: "8 years of Go experience.", Score: 0.9},
	}
	prompt := buildPrompt(testJob(), excerpts)
	for _, want := range []string{"Backend Engineer", "Acme", "Build APIs in Go", "8 years of Go experience", "JSON"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestBuildPromptNoExcerptsStillValid(t *testing.T) {
	prompt := buildPrompt(testJob(), nil)
	if !strings.Contains(prompt, "no excerpts") && !strings.Contains(prompt, "conservatively") {
		t.Errorf("expected prompt to instruct conservative scoring when no excerpts are available, got:\n%s", prompt)
	}
}

func TestParseVerdictPlainJSON(t *testing.T) {
	v, err := parseVerdict(`{"eligibility_pass":true,"language_pass":true,"location_pass":true,"technical_score":80,"experience_score":70,"behavioral_score":60,"career_score":90,"overall_score":79.5,"verdict":"Good Fit","rationale":"Strong Go background."}`)
	if err != nil {
		t.Fatalf("parseVerdict: %v", err)
	}
	if v.Verdict != "Good Fit" || v.OverallScore != 79.5 {
		t.Fatalf("unexpected verdict: %+v", v)
	}
}

func TestParseVerdictWrappedInMarkdownFence(t *testing.T) {
	resp := "Here is my assessment:\n```json\n{\"eligibility_pass\":true,\"language_pass\":true,\"location_pass\":true,\"technical_score\":50,\"experience_score\":50,\"behavioral_score\":50,\"career_score\":50,\"overall_score\":50,\"verdict\":\"Moderate Fit\",\"rationale\":\"Some overlap, e.g. {backend} skills.\"}\n```\nLet me know if you need more."
	v, err := parseVerdict(resp)
	if err != nil {
		t.Fatalf("parseVerdict: %v", err)
	}
	if v.Verdict != "Moderate Fit" {
		t.Fatalf("unexpected verdict: %+v", v)
	}
	if !strings.Contains(v.Rationale, "{backend}") {
		t.Fatalf("expected rationale containing a brace character to survive extraction intact, got %q", v.Rationale)
	}
}

func TestParseVerdictNoJSONFound(t *testing.T) {
	if _, err := parseVerdict("I cannot evaluate this."); err == nil {
		t.Fatal("expected error when no JSON object is present, got nil")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Strong Fit":   "strong-fit",
		"Poor Fit":     "poor-fit",
		"Ineligible":   "ineligible",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
