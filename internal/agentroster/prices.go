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
// testTurnTokensIn input and testTurnTokensOut output tokens with no cache
// discount.
const (
	testTurnTokensIn  = 15_000
	testTurnTokensOut = 100
)

type price struct{ in, out float64 } // USD per million tokens

var modelPrices = map[string]price{
	"claude-fable-5-1":  {10, 50},
	"claude-opus-5":     {5, 25},
	"claude-opus-4-7":   {5, 25},
	"claude-opus-4-6":   {5, 25},
	"claude-opus-4-5":   {5, 25},
	"claude-opus-4-1":   {15, 75},
	"claude-opus-4":     {15, 75},
	"claude-sonnet-5":   {2, 10},
	"claude-sonnet-4-6": {3, 15},
	"claude-sonnet-4-5": {3, 15},
	"claude-sonnet-4":   {3, 15},
	"claude-3-7-sonnet": {3, 15},
	"claude-3-5-sonnet": {3, 15},
	"claude-haiku-4-5":  {1, 5},
	"claude-haiku-4":    {0.8, 4},
	"claude-3-5-haiku":  {0.8, 4},
	"gpt-5":             {2.5, 10},
	"gpt-4o":            {2.5, 10},
	"gpt-4o-mini":       {0.15, 0.6},
	"gemini-2.5-pro":    {1.25, 10},
}

// modelAliases are the short ids runtimes list (claude's picker values).
var modelAliases = map[string]string{
	"fable":  "claude-fable-5-1",
	"opus":   "claude-opus-5",
	"sonnet": "claude-sonnet-5",
	"haiku":  "claude-haiku-4-5",
}

// runtimePrices price a runtime's "default" model, and a model the table
// doesn't know, at the runtime's usual paid default: claude at Opus 5 (the
// default on paid plans), codex at GPT-5, gemini at Gemini 2.5 Pro. Other
// runtimes route to many providers, so they stay unknown.
var runtimePrices = map[string]price{
	"claude": modelPrices["claude-opus-5"],
	"codex":  modelPrices["gpt-5"],
	"gemini": modelPrices["gemini-2.5-pro"],
}

// dateSuffix is a dated model id's "-20251001"; bracketSuffix a variant
// like claude's "opus[1m]".
var (
	dateSuffix    = regexp.MustCompile(`-\d{8}$`)
	bracketSuffix = regexp.MustCompile(`\[[^\]]*\]$`)
)

// TableTestCost is the built-in estimate for one test turn of runtime's
// model, and whether the table could price it.
func TableTestCost(runtime, model string) (float64, bool) {
	p, ok := lookupPrice(model)
	if !ok {
		p, ok = runtimePrices[runtime]
	}
	if !ok {
		return 0, false
	}
	return (testTurnTokensIn*p.in + testTurnTokensOut*p.out) / 1e6, true
}

// lookupPrice finds a model's price: by exact id (without a date or
// bracket suffix, or a "provider/" prefix), by alias, then by the longest
// table id the model id starts with ("gpt-5.5" is priced as "gpt-5").
func lookupPrice(model string) (price, bool) {
	id := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
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
		if strings.HasPrefix(id, k) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return price{}, false
	}
	return modelPrices[best], true
}
