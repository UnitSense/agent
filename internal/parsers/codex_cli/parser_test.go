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
	// 3 function_call + 1 custom_tool_call = 4 total tool invocations
	if a.ToolInvocations == nil || *a.ToolInvocations != 4 {
		t.Errorf("ToolInvocations = %v, want 4", a.ToolInvocations)
	}
	// 2 exec_command_end with exit_code=0 (ls, git commit) + 1 patch_apply_end success=true = 3
	// The 4th exec (gh pr create) had exit_code=1 so NOT counted
	if a.SuccessfulToolInvocations == nil || *a.SuccessfulToolInvocations != 3 {
		t.Errorf("SuccessfulToolInvocations = %v, want 3", a.SuccessfulToolInvocations)
	}
	// git commit matched with exit_code=0
	if a.Commits == nil || *a.Commits != 1 {
		t.Errorf("Commits = %v, want 1", a.Commits)
	}
	// gh pr create had exit_code=1, so NOT counted (gated on success)
	if a.PullRequests != nil && *a.PullRequests != 0 {
		t.Errorf("PullRequests = %v, want 0 (failed gh pr create not counted)", a.PullRequests)
	}
	// turn_context with model=gpt-5.1
	if a.ModelsUsed["gpt-5.1"] != 1 {
		t.Errorf("ModelsUsed[gpt-5.1] = %d, want 1", a.ModelsUsed["gpt-5.1"])
	}
}
