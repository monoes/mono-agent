package main

import (
	"encoding/json"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

func TestOptFieldAbsentNullValue(t *testing.T) {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"budget_usd":null,"max_rework_rounds":3,"lead":"a"}`), &raw)
	var p orgdesign.SectionPatch
	if err := optField(raw, "budget_usd", &p.BudgetUSD, &p.ClearBudget); err != nil || !p.ClearBudget || p.BudgetUSD != nil {
		t.Fatalf("null must clear: %+v %v", p, err)
	}
	if err := optField(raw, "max_rework_rounds", &p.MaxReworkRounds, &p.ClearMaxRework); err != nil || p.MaxReworkRounds == nil || *p.MaxReworkRounds != 3 || p.ClearMaxRework {
		t.Fatalf("value must set: %+v %v", p, err)
	}
	if err := optField(raw, "writes", &p.Writes, nil); err != nil || p.Writes != nil {
		t.Fatalf("absent must leave alone: %+v %v", p, err)
	}
	if err := optField(raw, "lead", &p.Lead, nil); err != nil || *p.Lead != "a" {
		t.Fatalf("lead: %v", err)
	}
	_ = json.Unmarshal([]byte(`{"lead":null}`), &raw)
	if err := optField(raw, "lead", &p.Lead, nil); err == nil {
		t.Fatal("a null lead must be refused, not ignored")
	}
}
