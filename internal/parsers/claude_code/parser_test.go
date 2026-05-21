package claude_code

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

func TestParseFixture(t *testing.T) {
	abs, _ := filepath.Abs("../../../testdata/claude_code")
	p := NewParser(abs)

	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	aggs, err := p.Aggregate(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("expected 1 day aggregate, got %d", len(aggs))
	}
	a := aggs[0]
	if a.DateString != "2026-05-21" {
		t.Errorf("DateString = %q, want 2026-05-21", a.DateString)
	}
	if a.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", a.SessionCount)
	}
	if a.ToolInvocations == nil || *a.ToolInvocations != 3 {
		t.Errorf("ToolInvocations = %v, want 3 (Edit + 2x Bash)", a.ToolInvocations)
	}
	if a.SuccessfulToolInvocations == nil || *a.SuccessfulToolInvocations != 2 {
		t.Errorf("SuccessfulToolInvocations = %v, want 2 (Edit, Bash:commit)", a.SuccessfulToolInvocations)
	}
	if a.LinesAdded == nil || *a.LinesAdded != 2 {
		t.Errorf("LinesAdded = %v, want 2", a.LinesAdded)
	}
	if a.LinesRemoved == nil || *a.LinesRemoved != 1 {
		t.Errorf("LinesRemoved = %v, want 1", a.LinesRemoved)
	}
	if a.Commits == nil || *a.Commits != 1 {
		t.Errorf("Commits = %v, want 1", a.Commits)
	}
	if a.PullRequests == nil || *a.PullRequests != 1 {
		t.Errorf("PullRequests = %v, want 1", a.PullRequests)
	}
	if a.ModelsUsed["claude-sonnet-4-7"] != 4 {
		t.Errorf("ModelsUsed[claude-sonnet-4-7] = %d, want 4", a.ModelsUsed["claude-sonnet-4-7"])
	}
	if a.MetricProvenance["lines_method"] != "edit_tool_input_estimate" {
		t.Errorf("MetricProvenance.lines_method missing")
	}
}

func TestParseEmptyDir(t *testing.T) {
	dir := t.TempDir()
	p := NewParser(dir)
	aggs, err := p.Aggregate(parsers.TimeWindow{
		From: time.Now().Add(-24 * time.Hour),
		To:   time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggs) != 0 {
		t.Fatalf("expected 0 aggs for empty dir, got %d", len(aggs))
	}
}

func TestProvider(t *testing.T) {
	p := NewParser("/tmp/none")
	if p.Provider() != "agent_claude_code" {
		t.Errorf("Provider = %q", p.Provider())
	}
	if p.Tool() != "claude_code" {
		t.Errorf("Tool = %q", p.Tool())
	}
	if p.ParserVersion() == "" {
		t.Errorf("ParserVersion empty")
	}
}
