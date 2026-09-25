// internal/matching/rubric_test.go
package matching

import (
	"strings"
	"testing"
)

// legacyRubricInstructions is the agent prompt exactly as it was hand-written
// before the weights and bands moved into Go constants. The rendered prompt
// must stay byte-identical so the agent path is unchanged.
const legacyRubricInstructions = `You are scoring a job application for fit against a candidate's profile.

Score in two phases:
1. HARD GATES (pass/fail, evaluated first):
   - Eligibility: does the candidate appear eligible to work in the job's location based on the profile information? (if unknown, assume pass)
   - Language: does the profile show proficiency in any language explicitly required by the job posting? (if the posting states no specific requirement, pass)
   - Location: is the job's location compatible with the candidate profile (remote, or a location the candidate could work from)? (if unclear, pass)
   If either eligibility or language fails, set overall_score to 0 and verdict to "Ineligible" and skip the dimension scores below (still include all fields, using 0 for unscored dimensions).

2. WEIGHTED DIMENSIONS (0-100 each, only if both eligibility and language gates pass):
   - technical_score (weight 30%): alignment of the candidate's technical skills/experience with the job's requirements.
   - experience_score (weight 25%): years and seniority level match.
   - behavioral_score (weight 15%): soft-skill/culture signals visible in the profile relative to what the posting implies.
   - career_score (weight 30%): whether this role is a sensible next step given the candidate's trajectory.
   overall_score = 0.30*technical_score + 0.25*experience_score + 0.15*behavioral_score + 0.30*career_score.
   verdict: "Strong Fit" (overall_score >= 80), "Good Fit" (>= 65), "Moderate Fit" (>= 50), "Weak Fit" (>= 30), "Poor Fit" (< 30).

Base every claim ONLY on the CANDIDATE PROFILE EXCERPTS section below — never invent experience, skills, or credentials not shown there.

Respond with ONLY a single JSON object, no markdown fencing, no other text, with exactly these fields:
{"eligibility_pass": bool, "language_pass": bool, "location_pass": bool, "technical_score": number, "experience_score": number, "behavioral_score": number, "career_score": number, "overall_score": number, "verdict": string, "rationale": string}`

func TestRubricInstructionsRenderedFromConstantsUnchanged(t *testing.T) {
	if rubricInstructions != legacyRubricInstructions {
		t.Fatalf("rendered rubric drifted from the legacy prompt:\n--- got ---\n%s\n--- want ---\n%s", rubricInstructions, legacyRubricInstructions)
	}
	for _, want := range []string{
		"technical_score (weight 30%)", "experience_score (weight 25%)",
		"behavioral_score (weight 15%)", "career_score (weight 30%)",
		"overall_score = 0.30*technical_score + 0.25*experience_score + 0.15*behavioral_score + 0.30*career_score",
		`"Strong Fit" (overall_score >= 80)`, `"Good Fit" (>= 65)`, `"Moderate Fit" (>= 50)`,
		`"Weak Fit" (>= 30)`, `"Poor Fit" (< 30)`, `verdict to "Ineligible"`,
		"(if unknown, assume pass)", "(if unclear, pass)",
	} {
		if !strings.Contains(rubricInstructions, want) {
			t.Errorf("rubric missing %q", want)
		}
	}
}

func TestWeightsSumToOne(t *testing.T) {
	sum := 0.0
	for _, d := range Dimensions {
		sum += d.Weight
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("weights sum to %v", sum)
	}
}

func TestVerdictForBandBoundaries(t *testing.T) {
	cases := []struct {
		score float64
		want  string
	}{
		{100, "Strong Fit"}, {80, "Strong Fit"}, {79.99, "Good Fit"},
		{65, "Good Fit"}, {64.99, "Moderate Fit"},
		{50, "Moderate Fit"}, {49.99, "Weak Fit"},
		{30, "Weak Fit"}, {29.99, "Poor Fit"}, {0, "Poor Fit"},
	}
	for _, c := range cases {
		if got := VerdictFor(c.score); got != c.want {
			t.Errorf("VerdictFor(%v) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestWeightedOverall(t *testing.T) {
	// 0.30*100 + 0.25*50 + 0.15*50 + 0.30*100 = 80 exactly (no float drift).
	if got := WeightedOverall(100, 50, 50, 100); got != 80 {
		t.Fatalf("WeightedOverall = %v, want 80", got)
	}
	if got := WeightedOverall(85, 80, 70, 90); got != 83 {
		t.Fatalf("WeightedOverall = %v, want 83", got)
	}
}
