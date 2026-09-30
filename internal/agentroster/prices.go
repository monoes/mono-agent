package agentroster

import (
	"regexp"
	"strings"
)

// A built-in price table for planning (monoes/mono-agent#272, #230): it
// prices a test turn that has no stored cost yet, so a first `agent
// validate --dry-run` can say what it will spend. A stored cost (reported by
// the runtime, or estimated by monomind) always wins over it.
//
// Source: monomind's pricing table, packages/@monomind/cli/src/pricing/
// model-pricing.ts at monoes/monomind 45d55c1e1 (2026-09-29), which follows
// the providers' list prices. Prices are USD per million tokens.
//
// The estimates are deliberately high. A one-word test turn still carries
// the runtime's system prompt and tool definitions, so it is priced as
// testTurnTokensIn input tokens at the cache-write rate (the dearest input
// rate: a first turn writes the prompt cache) and testTurnTokensOut output
// tokens. A model id the table can't price is left unknown rather than
// guessed at.
const (
	testTurnTokensIn  = 15_000
	testTurnTokensOut = 100
)

// price is USD per million tokens: input, output and cache-write input.
type price struct{ in, out, cw float64 }

var modelPrices = map[string]price{
	"claude-fable-5-1":  {10, 50, 12.5},
	"claude-opus-5":     {5, 25, 6.25},
	"claude-opus-4-7":   {5, 25, 6.25},
	"claude-opus-4-6":   {5, 25, 6.25},
	"claude-opus-4-5":   {5, 25, 6.25},
	"claude-opus-4-1":   {15, 75, 18.75},
	"claude-opus-4":     {15, 75, 18.75},
	"claude-sonnet-5":   {2, 10, 2.5},
	"claude-sonnet-4-6": {3, 15, 3.75},
	"claude-sonnet-4-5": {3, 15, 3.75},
	"claude-sonnet-4":   {3, 15, 3.75},
	"claude-3-7-sonnet": {3, 15, 3.75},
	"claude-3-5-sonnet": {3, 15, 3.75},
	"claude-haiku-4-5":  {1, 5, 1.25},
	"claude-haiku-4":    {0.8, 4, 1},
	"claude-3-5-haiku":  {0.8, 4, 1},
	"gpt-5":             {2.5, 10, 2.5},
	"gpt-4o":            {2.5, 10, 2.5},
	"gpt-4o-mini":       {0.15, 0.6, 0.15},
	"gemini-2.5-pro":    {1.25, 10, 1.25},
}

// modelAliases are the short ids runtimes list (claude's picker values).
var modelAliases = map[string]string{
	"fable":  "claude-fable-5-1",
	"opus":   "claude-opus-5",
	"sonnet": "claude-sonnet-5",
	"haiku":  "claude-haiku-4-5",
}

// runtimePrices price a runtime's "default" model (the test runs without
// --model) at the runtime's usual paid default: claude at Opus 5 (the
// default on paid plans), codex at GPT-5, gemini at Gemini 2.5 Pro. Other
// runtimes route to many providers, so they stay unknown.
var runtimePrices = map[string]price{
	"claude": modelPrices["claude-opus-5"],
	"codex":  modelPrices["gpt-5"],
	"gemini": modelPrices["gemini-2.5-pro"],
}

// pricierVariant is a suffix that makes a model dearer than the table id
// it starts with (gpt-5-pro is many times gpt-5): such an id is not priced
// by prefix.
var pricierVariant = regexp.MustCompile(`-(pro|max)\b`)

// dateSuffix is a dated model id's "-20251001"; bracketSuffix a variant
// like claude's "opus[1m]".
var (
	dateSuffix    = regexp.MustCompile(`-\d{8}$`)
	bracketSuffix = regexp.MustCompile(`\[[^\]]*\]$`)
)

// TableTestCost is the built-in estimate for one test turn of runtime's
// model, and whether the table could price it. The runtime's own price is
// used only for its "default" model.
func TableTestCost(runtime, model string) (float64, bool) {
	var p price
	var ok bool
	if model == "" || model == DefaultModel {
		p, ok = runtimePrices[runtime]
	} else {
		p, ok = lookupPrice(model)
	}
	if !ok {
		return 0, false
	}
	return (testTurnTokensIn*p.cw + testTurnTokensOut*p.out) / 1e6, true
}

// lookupPrice finds a model's price: by exact id (without an "@version",
// date or bracket suffix, or a "provider/" prefix), by alias, then by the
// longest table id the model id starts with ("gpt-5.5" is priced as
// "gpt-5"), unless the rest names a dearer variant ("gpt-5-pro").
func lookupPrice(model string) (price, bool) {
	id := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.Index(id, "@"); i >= 0 {
		id = id[:i]
	}
	id = dateSuffix.ReplaceAllString(bracketSuffix.ReplaceAllString(id, ""), "")
	if a, ok := modelAliases[id]; ok {
		id = a
	}
	if p, ok := modelPrices[id]; ok {
		return p, true
	}
	best := ""
	for k := range modelPrices {
		if strings.HasPrefix(id, k) && len(k) > len(best) && !pricierVariant.MatchString(id[len(k):]) {
			best = k
		}
	}
	if best == "" {
		return price{}, false
	}
	return modelPrices[best], true
}
