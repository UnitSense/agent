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
	if len(a.ToolCallsByName) != 2 {
		t.Errorf("ToolCallsByName has %d keys, want 2", len(a.ToolCallsByName))
	}
	if a.ToolCallsByName["Edit"] != 1 {
		t.Errorf("ToolCallsByName[Edit] = %d, want 1", a.ToolCallsByName["Edit"])
	}
	if a.ToolCallsByName["Bash"] != 2 {
		t.Errorf("ToolCallsByName[Bash] = %d, want 2", a.ToolCallsByName["Bash"])
	}
	// Token sums across the four assistant messages: input=100+120+80+90=390,
	// output=40+35+20+25=120, cache_read=200+250+150+180=780,
	// cache_creation=50+60+40+45=195.
	if a.InputTokens == nil || *a.InputTokens != 390 {
		t.Errorf("InputTokens = %v, want 390", a.InputTokens)
	}
	if a.OutputTokens == nil || *a.OutputTokens != 120 {
		t.Errorf("OutputTokens = %v, want 120", a.OutputTokens)
	}
	if a.CacheReadTokens == nil || *a.CacheReadTokens != 780 {
		t.Errorf("CacheReadTokens = %v, want 780", a.CacheReadTokens)
	}
	if a.CacheCreationTokens == nil || *a.CacheCreationTokens != 195 {
		t.Errorf("CacheCreationTokens = %v, want 195", a.CacheCreationTokens)
	}
}

func TestAggregateSessions(t *testing.T) {
	abs, _ := filepath.Abs("../../../testdata/claude_code")
	p := NewParser(abs)

	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	sessions, err := p.AggregateSessions(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("AggregateSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.SessionKey != "sess-1" {
		t.Errorf("SessionKey = %q, want sess-1", s.SessionKey)
	}
	if s.SessionDate.Format("2006-01-02") != "2026-05-21" {
		t.Errorf("SessionDate = %s, want 2026-05-21", s.SessionDate.Format("2006-01-02"))
	}
	// Tokens: same totals as daily aggregate (1 session covers all events)
	if s.InputTokens == nil || *s.InputTokens != 390 {
		t.Errorf("InputTokens = %v, want 390", s.InputTokens)
	}
	if s.OutputTokens == nil || *s.OutputTokens != 120 {
		t.Errorf("OutputTokens = %v, want 120", s.OutputTokens)
	}
	if s.CacheReadTokens == nil || *s.CacheReadTokens != 780 {
		t.Errorf("CacheReadTokens = %v, want 780", s.CacheReadTokens)
	}
	if s.CacheCreationTokens == nil || *s.CacheCreationTokens != 195 {
		t.Errorf("CacheCreationTokens = %v, want 195", s.CacheCreationTokens)
	}
	// Tool categories: Edit -> edit, 2 Bash -> 2 shell
	if s.ToolCounts["edit"] != 1 {
		t.Errorf("ToolCounts[edit] = %d, want 1", s.ToolCounts["edit"])
	}
	if s.ToolCounts["shell"] != 2 {
		t.Errorf("ToolCounts[shell] = %d, want 2", s.ToolCounts["shell"])
	}
	if s.SuccessfulToolInvocations == nil || *s.SuccessfulToolInvocations != 2 {
		t.Errorf("SuccessfulToolInvocations = %v, want 2", s.SuccessfulToolInvocations)
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
