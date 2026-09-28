package health

import (
	"context"
	"fmt"
	"strings"
)

// Browser automation packages: are they usable, and are their selectors
// still matching the sites (the health table `automation doctor` reads).
const (
	GroupAutomations = "automations"

	CheckAutomationPackages  = "automations.packages"
	CheckAutomationSelectors = "automations.selectors"

	FixRerecordSelector = "automations.rerecord"
	// FixInstallFromLibrary points at monoes.me: the app no longer ships
	// web automations, so a fresh install has none until the user adds them.
	FixInstallFromLibrary = "automations.library"
)

// NoAutomationsSummary and NoAutomationsDetail are the packages check's
// answer when nothing is installed (also what the app shows).
const (
	NoAutomationsSummary = "No web automations installed — install them from monoes.me"
	NoAutomationsDetail  = "Web automations (Instagram, LinkedIn, X, TikTok, Hacker News, Product Hunt, Gemini) are published on monoes.me. " +
		"List them with `monoagentcli library list --kind automation`, then install one with `monoagentcli library install automation <id>`."
)

// AutomationsInfo is what the automations checks read (all local).
type AutomationsInfo struct {
	Installed   int
	Unavailable []UnavailableAutomation
	// Selectors lists only the ones that need attention (broken, decaying).
	Selectors []SelectorProblem
}

type UnavailableAutomation struct {
	ID, Reason string
}

type SelectorProblem struct {
	AutomationID, Key string
	Status            string // broken | decaying
}

func automationChecks() []Check {
	return []Check{
		{ID: CheckAutomationPackages, Group: GroupAutomations, Title: "Browser automation packages",
			Features: []string{"browser automations"}, Run: checkAutomationPackages},
		{ID: CheckAutomationSelectors, Group: GroupAutomations, Title: "Automation selector health",
			Features: []string{"browser automations"}, DependsOn: []string{CheckAutomationPackages}, Run: checkAutomationSelectors},
	}
}

func automationFixes() []Fix {
	return []Fix{
		{FixInfo: FixInfo{ID: FixRerecordSelector, Label: "Re-record the selector by pointing at the element in the browser",
			Safety: SafetyManual, Command: "monoagentcli automation rerecord <automation> <selector>"},
			Apply: func(context.Context, *Env, func(string)) error { return fmt.Errorf("this needs to be done by hand") }},
		{FixInfo: FixInfo{ID: FixInstallFromLibrary, Label: "Install web automations from monoes.me",
			Safety: SafetyManual, Command: "monoagentcli library list --kind automation"},
			Apply: func(context.Context, *Env, func(string)) error {
				return fmt.Errorf("choose the automations to install: monoagentcli library install automation <id>")
			}},
	}
}

func checkAutomationPackages(ctx context.Context, env *Env) Result {
	if env.Automations == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	info, err := env.Automations(ctx)
	if err != nil {
		return Result{Status: StatusSkip, Summary: "cannot read the automation registry", Detail: err.Error()}
	}
	if info.Installed == 0 {
		return Result{Status: StatusInfo, Summary: NoAutomationsSummary, Detail: NoAutomationsDetail, FixID: FixInstallFromLibrary}
	}
	res := Result{Status: StatusOK, Summary: fmt.Sprintf("%d installed", info.Installed)}
	for _, u := range info.Unavailable {
		res.Children = append(res.Children, Result{ID: CheckAutomationPackages + "." + rowID(u.ID), Title: u.ID,
			Status: StatusWarn, Summary: orDefault(u.Reason, "unavailable")})
	}
	if n := len(info.Unavailable); n > 0 {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("%d of %d unavailable", n, info.Installed)
	}
	return res
}

func checkAutomationSelectors(ctx context.Context, env *Env) Result {
	if env.Automations == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	info, err := env.Automations(ctx)
	if err != nil {
		return Result{Status: StatusSkip, Summary: "cannot read selector health", Detail: err.Error()}
	}
	broken, decaying := 0, 0
	res := Result{}
	for _, s := range info.Selectors {
		child := Result{ID: CheckAutomationSelectors + "." + rowID(s.AutomationID) + "." + rowID(s.Key),
			Title: s.AutomationID + " › " + s.Key}
		if s.Status == "broken" {
			broken++
			child.Status, child.Summary = StatusWarn, "broken — no longer matches the page"
		} else {
			decaying++
			child.Status, child.Summary = StatusInfo, "decaying — failing or needing fallbacks lately"
		}
		child.FixID = FixRerecordSelector
		child.FixCommand = fmt.Sprintf("monoagentcli automation rerecord %s %s", s.AutomationID, s.Key)
		res.Children = append(res.Children, child)
	}
	switch {
	case broken > 0:
		res.Status, res.Summary = StatusWarn, fmt.Sprintf("%d broken, %d decaying", broken, decaying)
	case decaying > 0:
		res.Status, res.Summary = StatusInfo, fmt.Sprintf("%d decaying", decaying)
	default:
		res.Status, res.Summary = StatusOK, "no failing selectors"
	}
	return res
}

// rowID keeps a child row id stable and safe: letters, digits, '-' and '_'.
func rowID(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}
