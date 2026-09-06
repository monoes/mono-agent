// internal/matching/matching.go

// Package matching scores a job application's fit against the profile's
// ingested knowledge, delegating the scoring decision to a local agent
// runtime via monomind.Exec (the same pattern internal/nodes/agent's
// agent.ask node uses) rather than a direct LLM-provider call. See
// docs/mastermind/specs/2026-09-05-chat-matching-design.md.
package matching

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/monomind"
)

// FitVerdict is one scoring result for a job application.
type FitVerdict struct {
	EligibilityPass bool    `json:"eligibility_pass"`
	LanguagePass    bool    `json:"language_pass"`
	TechnicalScore  float64 `json:"technical_score"`
	ExperienceScore float64 `json:"experience_score"`
	BehavioralScore float64 `json:"behavioral_score"`
	CareerScore     float64 `json:"career_score"`
	LocationPass    bool    `json:"location_pass"`
	OverallScore    float64 `json:"overall_score"`
	Verdict         string  `json:"verdict"`
	Rationale       string  `json:"rationale"`
}

const rubricInstructions = `You are scoring a job application for fit against a candidate's profile.

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

// buildPrompt assembles the full evaluation prompt for job app, grounded
// in excerpts retrieved from the profile's knowledge base. If excerpts is
// empty, the prompt explicitly instructs conservative scoring rather than
// silently proceeding as if the profile were fully known.
func buildPrompt(app *applications.Application, excerpts []monomind.KnowledgeResult) string {
	var b strings.Builder
	b.WriteString(rubricInstructions)
	b.WriteString("\n\nJOB POSTING:\n")
	fmt.Fprintf(&b, "Title: %s\nCompany: %s\nLocation: %s\n\n%s\n",
		app.Job.Title, app.Job.Company, app.Job.Location, app.Job.Description)

	b.WriteString("\nCANDIDATE PROFILE EXCERPTS:\n")
	if len(excerpts) == 0 {
		b.WriteString("(no excerpts were found — no profile documents may be uploaded yet; score conservatively and say so explicitly in the rationale)\n")
	} else {
		for _, e := range excerpts {
			fmt.Fprintf(&b, "- (%s) %s\n", e.Path, e.Excerpt)
		}
	}
	return b.String()
}

// extractJSON scans s for top-level balanced {...} object(s), correctly
// skipping over braces that appear inside quoted string values (e.g.
// rationale text containing "{" or "}"). Local agent CLIs sometimes wrap
// their JSON response in prose or a markdown code fence despite being
// instructed not to — this makes parseVerdict robust to that without
// loosening the rubric itself.
//
// Prose can also contain a stray, unrelated balanced brace pair BEFORE the
// real verdict object (e.g. "the template uses {} placeholders..."). Since
// "{}" itself decodes as valid-but-empty JSON, naively returning the FIRST
// balanced group would silently hand back an empty object instead of the
// real verdict further down. To guard against that, extractJSON keeps
// scanning past a candidate that doesn't look like an actual verdict (no
// non-empty "verdict" field) and prefers the first candidate that does;
// if none do, it falls back to the first balanced candidate found (so
// parseVerdict's own field validation, and the error message, still have
// something concrete to report against).
func extractJSON(s string) (string, error) {
	var fallback string
	pos := 0
	for {
		idx := strings.Index(s[pos:], "{")
		if idx == -1 {
			break
		}
		start := pos + idx
		end := balancedBraceEnd(s, start)
		if end == -1 {
			break
		}
		candidate := s[start : end+1]
		if fallback == "" {
			fallback = candidate
		}
		if looksLikeVerdict(candidate) {
			return candidate, nil
		}
		pos = end + 1
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("matching: no balanced JSON object found in response")
}

// balancedBraceEnd returns the index of the '}' that closes the '{' at
// start (skipping brace-like characters inside quoted strings), or -1 if
// s has no balanced closer for it.
func balancedBraceEnd(s string, start int) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// looksLikeVerdict reports whether candidate decodes as JSON carrying a
// non-empty top-level "verdict" field — used by extractJSON to distinguish
// the real FitVerdict object from an incidental, unrelated brace pair
// (e.g. "{}") appearing earlier in an agent's prose response.
func looksLikeVerdict(candidate string) bool {
	var probe struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(candidate), &probe); err != nil {
		return false
	}
	return probe.Verdict != ""
}

// parseVerdict extracts and decodes a FitVerdict from an agent's raw
// response text. It treats a successfully-decoded but empty-"verdict"
// result as a parse failure rather than silently returning a zero-value
// verdict, closing the silent-corruption path even if extractJSON's
// brace-matching ever picks the wrong candidate.
func parseVerdict(responseText string) (*FitVerdict, error) {
	jsonStr, err := extractJSON(responseText)
	if err != nil {
		return nil, fmt.Errorf("matching: parsing verdict: %w (response was: %.200s)", err, responseText)
	}
	var v FitVerdict
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return nil, fmt.Errorf("matching: decoding verdict JSON: %w (extracted: %.200s)", err, jsonStr)
	}
	if v.Verdict == "" {
		return nil, fmt.Errorf("matching: parsed verdict is missing a required \"verdict\" field (extracted: %.200s)", jsonStr)
	}
	return &v, nil
}

// slugify lowercases s and replaces spaces with hyphens, for use as a tag
// suffix (e.g. "Strong Fit" -> "strong-fit", tagged as "fit:strong-fit").
func slugify(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "-")
}
