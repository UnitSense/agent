package codex_cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

func TestProvider(t *testing.T) {
	p := NewParser("/tmp/none")
	if p.Provider() != "agent_codex_cli" {
		t.Errorf("Provider = %q", p.Provider())
	}
}

func TestParseFixture(t *testing.T) {
	abs, _ := filepath.Abs("../../../testdata/codex_cli")
	p := NewParser(abs)
	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	aggs, err := p.Aggregate(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("expected 1 day, got %d", len(aggs))
	}
	a := aggs[0]
	if a.DateString != "2026-05-21" {
		t.Errorf("DateString = %q", a.DateString)
	}
	if a.SessionCount != 1 {
		t.Errorf("SessionCount = %d", a.SessionCount)
	}
	if a.ToolInvocations == nil || *a.ToolInvocations != 1 {
		t.Errorf("ToolInvocations = %v", a.ToolInvocations)
	}
	if a.SuccessfulToolInvocations == nil || *a.SuccessfulToolInvocations != 1 {
		t.Errorf("SuccessfulToolInvocations = %v", a.SuccessfulToolInvocations)
	}
	if a.ModelsUsed["gpt-5.1"] != 1 {
		t.Errorf("ModelsUsed[gpt-5.1] = %d", a.ModelsUsed["gpt-5.1"])
	}
}
