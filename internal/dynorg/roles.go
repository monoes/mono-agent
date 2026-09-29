package dynorg

import "strings"

// builtinRole is a role used when monomind's agent registry offers nothing
// for a brief (a folder without `.monomind/registry.json`, or no match).
type builtinRole struct {
	ID, Title, Category, Profile, Body string
	keywords                           []string
}

var builtinRoles = []builtinRole{
	{ID: "reviewer", Title: "Reviewer", Category: "review", Profile: ProfileResearch,
		keywords: []string{"review", "audit", "check", "inspect", "critique", "security"},
		Body:     "You review code and changes. Read carefully, find real defects (bugs, races, security holes, missing error handling), and report each with file:line, why it is wrong and a concrete fix. Do not edit files."},
	{ID: "researcher", Title: "Researcher", Category: "research", Profile: ProfileResearch,
		keywords: []string{"research", "find", "investigate", "explore", "look up", "compare", "document", "explain", "understand", "where"},
		Body:     "You investigate and report. Read the code, docs and the web as needed, and answer the brief with facts and their sources (file:line or URL). Do not edit files."},
	{ID: "tester", Title: "Tester", Category: "testing", Profile: ProfileCoding,
		keywords: []string{"test", "qa", "verify", "reproduce", "coverage", "e2e"},
		Body:     "You test. Write or run tests that prove the behavior the brief asks about, reproduce failures, and report exactly what passed and failed with the commands you ran."},
	{ID: "planner", Title: "Planner", Category: "planning", Profile: ProfileResearch,
		keywords: []string{"plan", "design", "architecture", "break down", "spec"},
		Body:     "You plan. Read the relevant code, then return a short, ordered plan with the files to change and the risks. Do not edit files."},
	{ID: "coder", Title: "Coder", Category: "engineering", Profile: ProfileCoding,
		Body: "You implement. Make the change the brief asks for with the smallest correct diff, following the code around it, and run the relevant tests."},
}

// builtinFor picks the built-in role whose keywords the brief mentions
// most; the coder when none match.
func builtinFor(brief string) builtinRole {
	b := strings.ToLower(brief)
	best, bestHits := builtinRoles[len(builtinRoles)-1], 0
	for _, r := range builtinRoles {
		hits := 0
		for _, k := range r.keywords {
			if strings.Contains(b, k) {
				hits++
			}
		}
		if hits > bestHits {
			best, bestHits = r, hits
		}
	}
	return best
}

func builtinByID(id string) (builtinRole, bool) {
	for _, r := range builtinRoles {
		if strings.EqualFold(r.ID, id) || strings.EqualFold(r.Title, id) {
			return r, true
		}
	}
	return builtinRole{}, false
}

// profileForCategory is the default access profile for an agent category.
func profileForCategory(category, id string) string {
	s := strings.ToLower(category + " " + id)
	switch {
	case containsAny(s, "automation", "workflow"):
		return ProfileAutomation
	case containsAny(s, "qa", "testing", "tester", "e2e", "browser", "accessibility", "evidence"):
		return ProfileQA
	case containsAny(s, "research", "review", "audit", "analysis", "planning", "planner", "architect", "docs", "writer"):
		return ProfileResearch
	}
	return ProfileCoding
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
