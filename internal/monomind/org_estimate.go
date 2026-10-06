package monomind

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrEstimateUnavailable means the pre-run estimate cannot be read safely.
var ErrEstimateUnavailable = errors.New("cost estimate unavailable")

// CostEstimate is monomind's pre-run cost estimate (`org run` prints it
// before any session starts), kept verbatim. monomind 2.24.1 has no
// estimate-only command and no JSON for it, so the text is the contract.
type CostEstimate struct {
	// Text is the block from "Cost estimate" through "Total estimate:".
	Text string `json:"text"`
	// StaleRates is monomind's own "stale rates" line, verbatim, or "" when
	// monomind did not print one.
	StaleRates string   `json:"stale_rates,omitempty"`
	TotalUSD   *float64 `json:"total_usd,omitempty"`
}

var totalEstimateRe = regexp.MustCompile(`Total estimate:\s+~\$([0-9.]+)`)

// ParseCostEstimate extracts the estimate block from `org run` output.
func ParseCostEstimate(out string) (*CostEstimate, bool) {
	lines := strings.Split(out, "\n")
	start, end := -1, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if start < 0 && t == "Cost estimate" {
			start = i
		}
		if start >= 0 && strings.HasPrefix(t, "Total estimate:") {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		return nil, false
	}
	est := &CostEstimate{Text: strings.TrimRight(strings.Join(lines[start:end+1], "\n"), " ")}
	for _, l := range lines[start : end+1] {
		if t := strings.TrimSpace(l); strings.Contains(strings.ToLower(t), "stale rates") {
			est.StaleRates = t
		}
	}
	if m := totalEstimateRe.FindStringSubmatch(lines[end]); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			est.TotalUSD = &v
		}
	}
	return est, true
}

// OrgCostEstimate reads the estimate without starting a run, by calling
// `org run <name> --yes --budget-usd=-1`: the estimate (always >= 0) exceeds
// a negative budget, so monomind prints it and aborts "before any tokens are
// spent". Two cases would start a run instead and are refused first: a live
// `org serve` daemon (monomind hands the request to it before the estimate)
// and an unsigned or changed org (monomind refuses, with no estimate).
func OrgCostEstimate(ctx context.Context, projectRoot, name string) (*CostEstimate, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return nil, fmt.Errorf("invalid org name %q", name)
	}
	if _, live := ReadServeHeartbeat(projectRoot); live {
		return nil, fmt.Errorf("%w: org serve is running and would take the request as a real run", ErrEstimateUnavailable)
	}
	bin, err := EnsureIn(ctx, projectRoot)
	if err != nil {
		return nil, err
	}
	if err := checkOrgSigned(ctx, projectRoot, name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEstimateUnavailable, err)
	}
	ctx, cancel := context.WithTimeout(ctx, orgTimeout)
	defer cancel()
	cmd := CommandContext(ctx, bin, "org", "run", name, "--yes", "--budget-usd=-1")
	inRoot(cmd, projectRoot)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	est, ok := ParseCostEstimate(out.String())
	if !ok {
		msg := strings.TrimSpace(out.String())
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		return nil, fmt.Errorf("%w: %s", ErrEstimateUnavailable, firstLine(msg))
	}
	return est, nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
