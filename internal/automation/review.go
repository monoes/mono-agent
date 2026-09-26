package automation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// buildReview is what the install confirmation shows (spec §6.3,
// contracts §8): permissions, scripts with their full text, and a
// plain-language list of capabilities.
func buildReview(p *Package, files map[string][]byte) Review {
	m := p.Manifest
	rv := Review{
		Source:        p.Source,
		Trust:         p.trust(),
		Native:        m.Requires.Native,
		CallActions:   nonNil(m.Permissions.CallActions),
		Domains:       nonNil(m.Site.Domains),
		Steps:         nonNil(m.Permissions.Steps),
		Scripts:       nonNil(p.ScriptFiles()),
		ScriptSources: map[string]string{},
		Downloads:     m.Permissions.Downloads,
		Tier:          m.Policy.Tier,
		ActionEffects: map[string]string{},
		Visibility:    map[string][]string{},
		Files:         fileInfos(files),
	}
	if rv.Tier == "" {
		rv.Tier = "standard"
	}
	rv.SocialPlatform = socialPlatform(m)
	rv.ComputedTier = "standard"
	if rv.SocialPlatform != "" {
		rv.ComputedTier = "social"
	}
	if m.Publisher != nil {
		rv.Publisher = m.Publisher.Name
	}
	if m.Login != nil {
		rv.LoginURL = m.Login.URL
	}
	for _, s := range rv.Scripts {
		src, _ := p.Script(s)
		rv.ScriptSources[s] = src
	}
	for _, a := range m.Actions {
		effect := "undeclared"
		def, err := p.Action(a)
		if err == nil && def.SideEffects != "" {
			effect = def.SideEffects
		}
		rv.ActionEffects[a] = effect
		if err == nil {
			for _, k := range def.Visibility {
				if !contains(rv.Visibility[k], a) {
					rv.Visibility[k] = append(rv.Visibility[k], a)
				}
			}
		}
	}
	rv.Capabilities = capabilities(m, rv)
	return rv
}

// permits reports whether a step type is allowed by permissions.steps
// (exact entries or "prefix*" globs; empty = everything).
func permits(steps []string, typ string) bool {
	if len(steps) == 0 {
		return true
	}
	for _, s := range steps {
		s = strings.TrimSpace(s)
		if s == typ || s == "*" {
			return true
		}
		if prefix, ok := strings.CutSuffix(s, "*"); ok && strings.HasPrefix(typ, prefix) {
			return true
		}
	}
	return false
}

// capabilities describes in plain words what installing the package lets
// it do, most dangerous first.
func capabilities(m Manifest, rv Review) []string {
	var out []string
	steps := m.Permissions.Steps
	if len(rv.Scripts) > 0 || (permits(steps, "page_script") && len(m.Permissions.Scripts) > 0) {
		out = append(out, fmt.Sprintf("can run scripts in the page that can read site data and send it anywhere (%s)", strings.Join(rv.Scripts, ", ")))
	}
	if len(m.Site.Domains) == 0 {
		out = append(out, "can open any website (no domain allowlist)")
	} else {
		out = append(out, "can open and act on: "+strings.Join(m.Site.Domains, ", "))
	}
	if len(steps) == 0 {
		out = append(out, "may use every step type (no step allowlist)")
	}
	if permits(steps, "http_fetch_in_page") && len(steps) > 0 {
		out = append(out, "can send web requests with your logged-in session on its sites")
	}
	if permits(steps, "upload") && len(steps) > 0 {
		out = append(out, "can upload local files to its sites")
	}
	if m.Permissions.Downloads || (permits(steps, "download") && len(steps) > 0) {
		out = append(out, "can download files to your computer")
	}
	effects := map[string][]string{}
	for a, e := range rv.ActionEffects {
		effects[e] = append(effects[e], a)
	}
	for _, e := range []struct{ level, text string }{
		{"destructive", "can delete content as you"},
		{"message", "can send messages as you"},
		{"write", "can create or change content as you"},
	} {
		if as := effects[e.level]; len(as) > 0 {
			sort.Strings(as)
			out = append(out, fmt.Sprintf("%s (%s)", e.text, strings.Join(as, ", ")))
		}
	}
	out = append(out, visibilityCapabilities(rv.Visibility)...)
	if len(m.Permissions.CallActions) > 0 {
		out = append(out, "can run actions of other automations: "+strings.Join(m.Permissions.CallActions, ", "))
	}
	if m.Login != nil && m.Login.URL != "" {
		out = append(out, "uses your logged-in browser session on its sites")
	}
	if m.Requires.Native != "" {
		out = append(out, fmt.Sprintf("uses the built-in %s bot compiled into this app", m.Requires.Native))
	}
	if rv.ComputedTier == "social" {
		out = append(out, fmt.Sprintf("automates a social platform (%s): subject to the usage policy", rv.SocialPlatform))
	}
	return out
}

// reviewChanges is the permission/script diff of an update.
func reviewChanges(old, next *Package) *ReviewChanges {
	ch := &ReviewChanges{
		AddedDomains:     added(old.Manifest.Site.Domains, next.Manifest.Site.Domains),
		AddedSteps:       added(old.Manifest.Permissions.Steps, next.Manifest.Permissions.Steps),
		AddedCallActions: added(old.Manifest.Permissions.CallActions, next.Manifest.Permissions.CallActions),
	}
	oldScripts := old.ScriptFiles()
	for _, s := range next.ScriptFiles() {
		if !contains(oldScripts, s) {
			ch.AddedScripts = append(ch.AddedScripts, s)
			continue
		}
		a, _ := old.Script(s)
		b, _ := next.Script(s)
		if a != b {
			ch.ChangedScripts = append(ch.ChangedScripts, s)
		}
	}
	return ch
}

func added(old, next []string) []string {
	var out []string
	for _, s := range next {
		if !contains(old, s) {
			out = append(out, s)
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// visibilityPhrases describe action.VisibilityKinds in plain words.
var visibilityPhrases = map[string]string{
	"profile_view_visible_to_owner": "visits profiles — the profile's owner can see your visit",
	"story_view_visible_to_owner":   "views stories — the story's owner sees you among its viewers",
	"search_may_be_saved":           "runs searches — the site may keep them in your account's search history",
	"video_view_counted":            "opens videos — each visit may count as a view",
	"account_preference_prompt":     "answers site prompts — the site may remember your answer",
}

// visibilityCapabilities is one line per visibility kind (deduplicated
// across actions), in action.VisibilityKinds order, naming the actions.
func visibilityCapabilities(vis map[string][]string) []string {
	var out []string
	seen := map[string]bool{}
	line := func(kind string) {
		as := append([]string(nil), vis[kind]...)
		sort.Strings(as)
		text, ok := visibilityPhrases[kind]
		if !ok {
			text = "leaves a visible trace on the site (" + kind + ")"
		}
		out = append(out, fmt.Sprintf("%s (%s)", text, strings.Join(as, ", ")))
		seen[kind] = true
	}
	for _, kind := range action.VisibilityKinds {
		if len(vis[kind]) > 0 {
			line(kind)
		}
	}
	var rest []string
	for kind, as := range vis {
		if !seen[kind] && len(as) > 0 {
			rest = append(rest, kind)
		}
	}
	sort.Strings(rest)
	for _, kind := range rest {
		line(kind)
	}
	return out
}
