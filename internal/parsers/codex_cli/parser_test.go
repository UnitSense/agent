package codex_cli

import (
	"fmt"
	"os"
	"os/exec"
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

// TestParserVersionBump verifies the version constant reflects v0.6.0.
func TestParserVersionBump(t *testing.T) {
	if ParserVersionConst != "codex-cli-parser-0.6.0" {
		t.Errorf("ParserVersionConst = %q, want codex-cli-parser-0.6.0", ParserVersionConst)
	}
}

func TestAggregateSessions(t *testing.T) {
	abs, _ := filepath.Abs("../../../testdata/codex_cli")
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
	if s.SessionKey != "cdx-test-1" {
		t.Errorf("SessionKey = %q, want cdx-test-1", s.SessionKey)
	}
	// 2 non-null token_count events: input=800, output=300 (incl reasoning), cache_read=1600
	if s.InputTokens == nil || *s.InputTokens != 800 {
		t.Errorf("InputTokens = %v, want 800", s.InputTokens)
	}
	if s.OutputTokens == nil || *s.OutputTokens != 300 {
		t.Errorf("OutputTokens = %v, want 300", s.OutputTokens)
	}
	if s.CacheReadTokens == nil || *s.CacheReadTokens != 1600 {
		t.Errorf("CacheReadTokens = %v, want 1600", s.CacheReadTokens)
	}
	if s.CacheCreationTokens != nil {
		t.Errorf("CacheCreationTokens should be nil for Codex; got %v", s.CacheCreationTokens)
	}
	// 3 exec_command + 1 custom_tool_call. exec_command -> shell, apply_patch -> edit
	if s.ToolCounts["shell"] != 3 {
		t.Errorf("ToolCounts[shell] = %d, want 3", s.ToolCounts["shell"])
	}
	if s.ToolCounts["edit"] != 1 {
		t.Errorf("ToolCounts[edit] = %d, want 1", s.ToolCounts["edit"])
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
	if a.ToolCallsByName["exec_command"] != 3 {
		t.Errorf("ToolCallsByName[exec_command] = %d, want 3", a.ToolCallsByName["exec_command"])
	}
	if a.ToolCallsByName["apply_patch"] != 1 {
		t.Errorf("ToolCallsByName[apply_patch] = %d, want 1", a.ToolCallsByName["apply_patch"])
	}
	// Two token_count events with non-null info:
	//   t1: input=500 cached_input=1000 output=150 reasoning=50
	//   t2: input=300 cached_input=600  output=80  reasoning=20
	// Sums: input=800, cache_read=1600, output=150+50+80+20=300
	// One token_count event had info=null and must be ignored.
	if a.InputTokens == nil || *a.InputTokens != 800 {
		t.Errorf("InputTokens = %v, want 800", a.InputTokens)
	}
	if a.OutputTokens == nil || *a.OutputTokens != 300 {
		t.Errorf("OutputTokens = %v, want 300 (output + reasoning)", a.OutputTokens)
	}
	if a.CacheReadTokens == nil || *a.CacheReadTokens != 1600 {
		t.Errorf("CacheReadTokens = %v, want 1600", a.CacheReadTokens)
	}
	// Codex does not emit cache_creation tokens.
	if a.CacheCreationTokens != nil {
		t.Errorf("CacheCreationTokens = %v, want nil (Codex doesn't emit)", a.CacheCreationTokens)
	}
}

// TestAggregateSessionsGitHintsDisabled verifies that git hints are NOT
// populated when enableGitHints is false (the default).
func TestAggregateSessionsGitHintsDisabled(t *testing.T) {
	abs, _ := filepath.Abs("../../../testdata/codex_cli")
	p := NewParserWithOptions(abs, false)

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
	if s.RepoRemoteHash != "" {
		t.Errorf("RepoRemoteHash should be empty when git hints disabled, got %q", s.RepoRemoteHash)
	}
	if s.BranchName != "" {
		t.Errorf("BranchName should be empty when git hints disabled, got %q", s.BranchName)
	}
	if len(s.CommitSHAs) != 0 {
		t.Errorf("CommitSHAs should be empty when git hints disabled, got %v", s.CommitSHAs)
	}
}

// TestAggregateSessionsGitHintsEnabled verifies that git hints ARE populated
// when enableGitHints is true. The session_meta CWD field is used as the
// workspace path — for Codex CLI this is straightforward (no encoding needed).
func TestAggregateSessionsGitHintsEnabled(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	workspaceDir := t.TempDir()

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", workspaceDir)
	runGit("-C", workspaceDir, "checkout", "-b", "codex-hints-branch")
	os.WriteFile(filepath.Join(workspaceDir, "f.txt"), []byte("x"), 0644)
	runGit("-C", workspaceDir, "add", "f.txt")

	commitTime := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
	cmd := exec.Command("git", "-C", workspaceDir, "commit", "-m", "codex test")
	cmd.Env = append(gitEnv, "GIT_AUTHOR_DATE="+commitTime, "GIT_COMMITTER_DATE="+commitTime)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// Build a JSONL session file that points cwd at our temp workspace dir
	rootDir := t.TempDir()
	sessionTime := time.Now().UTC().Add(-1 * time.Hour)
	sessionMeta := fmt.Sprintf(
		`{"timestamp":"%s","type":"session_meta","payload":{"id":"cdx-hints-1","cwd":"%s","originator":"codex-tui"}}`,
		sessionTime.Format(time.RFC3339),
		workspaceDir,
	)
	assistantLine := fmt.Sprintf(
		`{"timestamp":"%s","type":"turn_context","payload":{"model":"gpt-test"}}`,
		sessionTime.Add(time.Minute).Format(time.RFC3339),
	)
	jsonlPath := filepath.Join(rootDir, "session.jsonl")
	content := sessionMeta + "\n" + assistantLine + "\n"
	if err := os.WriteFile(jsonlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewParserWithOptions(rootDir, true)
	from := sessionTime.Add(-1 * time.Hour)
	to := sessionTime.Add(3 * time.Hour)
	sessions, err := p.AggregateSessions(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("AggregateSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.BranchName != "codex-hints-branch" {
		t.Errorf("BranchName = %q, want codex-hints-branch", s.BranchName)
	}
	// No remote → hash empty
	if s.RepoRemoteHash != "" {
		t.Errorf("RepoRemoteHash = %q, want empty (no remote)", s.RepoRemoteHash)
	}
}
