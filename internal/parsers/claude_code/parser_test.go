package claude_code

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if p.ParserVersion() != ParserVersionConst {
		t.Errorf("ParserVersion = %q, want %q", p.ParserVersion(), ParserVersionConst)
	}
}

// TestParserVersionBump verifies the version constant reflects v0.8.0
// (0.8.0 added estimated rate-limit/quota — see ratelimits.go).
func TestParserVersionBump(t *testing.T) {
	if ParserVersionConst != "claude-code-parser-0.8.0" {
		t.Errorf("ParserVersionConst = %q, want claude-code-parser-0.8.0", ParserVersionConst)
	}
}

// TestAggregateSessionsGitHintsDisabled verifies that git hints are NOT
// populated when enableGitHints is false (the default).
func TestAggregateSessionsGitHintsDisabled(t *testing.T) {
	abs, _ := filepath.Abs("../../../testdata/claude_code")
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
// when enableGitHints is true, using a real git repo. The test builds the
// project directory structure using a deterministic path under /tmp with no
// hyphens (so the Claude Code path encoding round-trips correctly).
func TestAggregateSessionsGitHintsEnabled(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Use a fixed test directory under /tmp with no hyphens so that the
	// encoding (/ → -) round-trips without ambiguity.
	// Use a unique suffix per test run to avoid collisions.
	testID := fmt.Sprintf("%d", time.Now().UnixNano())
	rootDir := fmt.Sprintf("/tmp/unitsensetest%s/projects", testID)
	workspaceDir := fmt.Sprintf("/tmp/unitsensetest%s/workspace/myproject", testID)
	t.Cleanup(func() { os.RemoveAll(fmt.Sprintf("/tmp/unitsensetest%s", testID)) })

	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		t.Fatal(err)
	}

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
	runGit("-C", workspaceDir, "checkout", "-b", "hints-branch")
	os.WriteFile(filepath.Join(workspaceDir, "f.txt"), []byte("x"), 0644)
	runGit("-C", workspaceDir, "add", "f.txt")

	commitTime := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
	cmd := exec.Command("git", "-C", workspaceDir, "commit", "-m", "test")
	cmd.Env = append(gitEnv, "GIT_AUTHOR_DATE="+commitTime, "GIT_COMMITTER_DATE="+commitTime)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// Encode the workspace path: replace "/" with "-".
	// Since workspaceDir is /tmp/unitsensetest.../workspace/myproject (no hyphens
	// except within the test ID which is all digits), this round-trips correctly.
	encodedDirName := ""
	for _, c := range workspaceDir {
		if c == '/' {
			encodedDirName += "-"
		} else {
			encodedDirName += string(c)
		}
	}

	projDir := filepath.Join(rootDir, encodedDirName)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	sessionTime := time.Now().UTC().Add(-1 * time.Hour)
	sessionJSON := `{"type":"assistant","sessionId":"git-test-sess","timestamp":"` +
		sessionTime.Format(time.RFC3339) + `","message":{"model":"claude-test","content":[],"usage":{"input_tokens":10,"output_tokens":5}}}`
	jsonlPath := filepath.Join(projDir, "session.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(sessionJSON+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewParserWithOptions(rootDir, true)
	from := sessionTime.Add(-1 * time.Hour)
	to := sessionTime.Add(2 * time.Hour)
	sessions, err := p.AggregateSessions(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("AggregateSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.BranchName != "hints-branch" {
		t.Errorf("BranchName = %q, want hints-branch", s.BranchName)
	}
	// No remote configured → hash empty
	if s.RepoRemoteHash != "" {
		t.Errorf("RepoRemoteHash = %q, want empty (no remote)", s.RepoRemoteHash)
	}
}

// TestAggregateSessionsGitBranchFromJSONL verifies that when enableGitHints is
// true and the JSONL event carries a gitBranch field, BranchName is taken from
// the event rather than from git shell-out. The cwd in the fixture points to a
// path that does not exist on disk (/Users/test-user/my-project), so git will
// fail silently — but BranchName must still be set from the JSONL value.
// This also validates that paths containing hyphens (which would decode wrong)
// are handled correctly via the cwd field.
func TestAggregateSessionsGitBranchFromJSONL(t *testing.T) {
	abs, _ := filepath.Abs("../../../testdata/claude_code")
	p := NewParserWithOptions(abs, true)

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
	// gitBranch from the JSONL attachment event must be used, overriding any
	// failed git shell-out (the cwd /Users/test-user/my-project does not exist).
	if s.BranchName != "feature/my-hyphenated-branch" {
		t.Errorf("BranchName = %q, want feature/my-hyphenated-branch (from JSONL gitBranch)", s.BranchName)
	}
	// cwd does not exist → git fails → no remote hash or commit SHAs
	if s.RepoRemoteHash != "" {
		t.Errorf("RepoRemoteHash = %q, want empty (cwd not a git repo)", s.RepoRemoteHash)
	}
}

// TestAggregateSessionsCwdPrecedence verifies that when a JSONL event carries a
// cwd field and the project directory also decodes to a valid path, the cwd from
// the event is preferred as the workspace path for git hints.
func TestAggregateSessionsCwdPrecedence(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	testID := fmt.Sprintf("%d", time.Now().UnixNano())
	rootDir := fmt.Sprintf("/tmp/unitsensetest%s/projects", testID)
	// This is the workspace that the JSONL cwd will point to.
	cwdWorkspace := fmt.Sprintf("/tmp/unitsensetest%s/cwd-workspace", testID)
	// This is a decoy workspace that the decoded projectDir would point to.
	decoyWorkspace := fmt.Sprintf("/tmp/unitsensetest%s/decoy-workspace", testID)
	t.Cleanup(func() { os.RemoveAll(fmt.Sprintf("/tmp/unitsensetest%s", testID)) })

	for _, dir := range []string{cwdWorkspace, decoyWorkspace, rootDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
		}
	}

	// Init cwdWorkspace on branch "cwd-branch".
	runGit(".", "init", cwdWorkspace)
	runGit(cwdWorkspace, "checkout", "-b", "cwd-branch")
	os.WriteFile(filepath.Join(cwdWorkspace, "f.txt"), []byte("x"), 0644)
	runGit(cwdWorkspace, "add", "f.txt")
	commitTime := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
	cmd := exec.Command("git", "-C", cwdWorkspace, "commit", "-m", "test")
	cmd.Env = append(gitEnv, "GIT_AUTHOR_DATE="+commitTime, "GIT_COMMITTER_DATE="+commitTime)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit in cwd-workspace: %v\n%s", err, out)
	}

	// Init decoyWorkspace on branch "decoy-branch".
	runGit(".", "init", decoyWorkspace)
	runGit(decoyWorkspace, "checkout", "-b", "decoy-branch")
	os.WriteFile(filepath.Join(decoyWorkspace, "g.txt"), []byte("y"), 0644)
	runGit(decoyWorkspace, "add", "g.txt")
	cmd = exec.Command("git", "-C", decoyWorkspace, "commit", "-m", "decoy")
	cmd.Env = append(gitEnv, "GIT_AUTHOR_DATE="+commitTime, "GIT_COMMITTER_DATE="+commitTime)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit in decoy-workspace: %v\n%s", err, out)
	}

	// Encode the decoy workspace path as the project directory name so that
	// the old decode logic would point to decoyWorkspace.
	encodedDirName := strings.ReplaceAll(decoyWorkspace, "/", "-")
	projDir := filepath.Join(rootDir, encodedDirName)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	sessionTime := time.Now().UTC().Add(-1 * time.Hour)
	// The event carries cwd pointing to cwdWorkspace, which should win.
	sessionJSON := `{"type":"attachment","sessionId":"cwd-test-sess","timestamp":"` +
		sessionTime.Format(time.RFC3339) + `","cwd":"` + cwdWorkspace + `","gitBranch":"cwd-branch"}` + "\n" +
		`{"type":"assistant","sessionId":"cwd-test-sess","timestamp":"` +
		sessionTime.Add(time.Second).Format(time.RFC3339) + `","message":{"model":"claude-test","content":[],"usage":{"input_tokens":5,"output_tokens":2}}}`
	jsonlPath := filepath.Join(projDir, "session.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(sessionJSON+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewParserWithOptions(rootDir, true)
	from := sessionTime.Add(-1 * time.Hour)
	to := sessionTime.Add(2 * time.Hour)
	sessions, err := p.AggregateSessions(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("AggregateSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	// cwd from JSONL points to cwdWorkspace which is on "cwd-branch", not "decoy-branch".
	if s.BranchName != "cwd-branch" {
		t.Errorf("BranchName = %q, want cwd-branch (from JSONL cwd, not decoded projectDir)", s.BranchName)
	}
}
