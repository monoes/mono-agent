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

// TestParseVerdictSkipsStrayLeadingBraces is a regression test for Finding
// 2: a stray, unrelated balanced brace pair ("{}") appearing in prose
// BEFORE the real verdict object must not be mistaken for the verdict.
// Naive first-balanced-group extraction would return "{}" itself (valid,
// empty JSON) and parseVerdict would silently succeed with an all
// zero-value FitVerdict instead of surfacing the real one.
func TestParseVerdictSkipsStrayLeadingBraces(t *testing.T) {
	resp := `Here's my note: the template uses {} placeholders in this posting. {"eligibility_pass":true,"language_pass":true,"location_pass":true,"technical_score":85,"experience_score":75,"behavioral_score":60,"career_score":90,"overall_score":82,"verdict":"Strong Fit","rationale":"Excellent alignment."}`
	v, err := parseVerdict(resp)
	if err != nil {
		t.Fatalf("parseVerdict: %v", err)
	}
	if v.Verdict != "Strong Fit" {
		t.Fatalf("expected the real verdict object past the stray {} to be extracted, got: %+v", v)
	}
	if v.OverallScore != 82 {
		t.Fatalf("unexpected overall score: %+v", v)
	}
}

// TestParseVerdictRejectsEmptyVerdictField guards the belt-and-suspenders
// validation in parseVerdict itself: even if extraction ever picks a
// candidate that decodes successfully but has no "verdict" field set, that
// must be treated as an error rather than a silently-accepted zero-value
// FitVerdict.
func TestParseVerdictRejectsEmptyVerdictField(t *testing.T) {
	if _, err := parseVerdict("{}"); err == nil {
		t.Fatal("expected an error for a valid-but-empty JSON object with no verdict field, got nil")
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
