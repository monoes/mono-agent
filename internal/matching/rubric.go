// internal/matching/rubric.go
package matching

import (
	"fmt"
	"math"
	"strings"
)

// Dimension is one weighted scoring dimension of the fit rubric.
type Dimension struct {
	Key         string  // short id, also the jev question id: technical, experience, …
	Field       string  // FitVerdict JSON field: technical_score, …
	Weight      float64 // share of overall_score; all weights sum to 1
	Description string  // rubric wording, shared by the agent prompt and the jev level texts
}

// Rubric weights (plan D7: arithmetic stays in Go, both backends use these).
const (
	WeightTechnical  = 0.30
	WeightExperience = 0.25
	WeightBehavioral = 0.15
	WeightCareer     = 0.30
)

// Dimensions lists the weighted dimensions in rubric order.
var Dimensions = []Dimension{
	{Key: "technical", Field: "technical_score", Weight: WeightTechnical,
		Description: "alignment of the candidate's technical skills/experience with the job's requirements."},
	{Key: "experience", Field: "experience_score", Weight: WeightExperience,
		Description: "years and seniority level match."},
	{Key: "behavioral", Field: "behavioral_score", Weight: WeightBehavioral,
		Description: "soft-skill/culture signals visible in the profile relative to what the posting implies."},
	{Key: "career", Field: "career_score", Weight: WeightCareer,
		Description: "whether this role is a sensible next step given the candidate's trajectory."},
}

// Verdict labels.
const (
	VerdictStrong     = "Strong Fit"
	VerdictGood       = "Good Fit"
	VerdictModerate   = "Moderate Fit"
	VerdictWeak       = "Weak Fit"
	VerdictPoor       = "Poor Fit"
	VerdictIneligible = "Ineligible"
)

// Band maps a minimum overall score (inclusive) to a verdict.
type Band struct {
	Min     float64
	Verdict string
}

// Bands are the verdict bands, highest first; below the last Min is VerdictPoor.
var Bands = []Band{
	{80, VerdictStrong},
	{65, VerdictGood},
	{50, VerdictModerate},
	{30, VerdictWeak},
}

// VerdictFor returns the verdict band for an overall score (gates aside).
func VerdictFor(overall float64) string {
	for _, b := range Bands {
		if overall >= b.Min {
			return b.Verdict
		}
	}
	return VerdictPoor
}

// WeightedOverall combines the four 0–100 dimension scores with the rubric
// weights, rounded to two decimals so band boundaries are not lost to float
// drift (0.30*100 + 0.25*50 + 0.15*50 + 0.30*100 is exactly 80).
func WeightedOverall(technical, experience, behavioral, career float64) float64 {
	v := WeightTechnical*technical + WeightExperience*experience + WeightBehavioral*behavioral + WeightCareer*career
	return math.Round(v*100) / 100
}

func pct(w float64) string { return fmt.Sprintf("%.0f%%", w*100) }

// renderRubricInstructions renders the agent prompt from Dimensions and Bands.
func renderRubricInstructions() string {
	var dims, formula []string
	for _, d := range Dimensions {
		dims = append(dims, fmt.Sprintf("   - %s (weight %s): %s", d.Field, pct(d.Weight), d.Description))
		formula = append(formula, fmt.Sprintf("%.2f*%s", d.Weight, d.Field))
	}
	var bands []string
	for i, b := range Bands {
		if i == 0 {
			bands = append(bands, fmt.Sprintf("%q (overall_score >= %g)", b.Verdict, b.Min))
		} else {
			bands = append(bands, fmt.Sprintf("%q (>= %g)", b.Verdict, b.Min))
		}
	}
	bands = append(bands, fmt.Sprintf("%q (< %g)", VerdictPoor, Bands[len(Bands)-1].Min))

	return `You are scoring a job application for fit against a candidate's profile.

Score in two phases:
1. HARD GATES (pass/fail, evaluated first):
   - Eligibility: does the candidate appear eligible to work in the job's location based on the profile information? (if unknown, assume pass)
   - Language: does the profile show proficiency in any language explicitly required by the job posting? (if the posting states no specific requirement, pass)
   - Location: is the job's location compatible with the candidate profile (remote, or a location the candidate could work from)? (if unclear, pass)
   If either eligibility or language fails, set overall_score to 0 and verdict to "` + VerdictIneligible + `" and skip the dimension scores below (still include all fields, using 0 for unscored dimensions).

2. WEIGHTED DIMENSIONS (0-100 each, only if both eligibility and language gates pass):
` + strings.Join(dims, "\n") + `
   overall_score = ` + strings.Join(formula, " + ") + `.
   verdict: ` + strings.Join(bands, ", ") + `.

Base every claim ONLY on the CANDIDATE PROFILE EXCERPTS section below — never invent experience, skills, or credentials not shown there.

Respond with ONLY a single JSON object, no markdown fencing, no other text, with exactly these fields:
{"eligibility_pass": bool, "language_pass": bool, "location_pass": bool, "technical_score": number, "experience_score": number, "behavioral_score": number, "career_score": number, "overall_score": number, "verdict": string, "rationale": string}`
}
