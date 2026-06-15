package claude_code

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
	"github.com/UnitSense/agent/internal/parsers/githints"
)

const ParserVersionConst = "claude-code-parser-0.8.0" // 0.8.0: estimated rate-limit/quota (ratelimits.go)

// Tool category buckets. Anything not matching defaults to "other".
var toolCategory = map[string]string{
	"Read":         "read",
	"Glob":         "read",
	"Grep":         "search",
	"Edit":         "edit",
	"MultiEdit":    "edit",
	"NotebookEdit": "edit",
	"Write":        "write",
	"Bash":         "shell",
	"BashOutput":   "shell",
	"KillShell":    "shell",
	"WebFetch":     "fetch",
	"WebSearch":    "search",
}

func categorize(name string) string {
	if c, ok := toolCategory[name]; ok {
		return c
	}
	return "other"
}

type Parser struct {
	rootDir        string
	enableGitHints bool
}

func NewParser(rootDir string) *Parser {
	return &Parser{rootDir: rootDir}
}

// NewParserWithOptions creates a parser with optional git hints support.
func NewParserWithOptions(rootDir string, enableGitHints bool) *Parser {
	return &Parser{rootDir: rootDir, enableGitHints: enableGitHints}
}

func (p *Parser) Provider() string      { return "agent_claude_code" }
func (p *Parser) Tool() string          { return "claude_code" }
func (p *Parser) ParserVersion() string { return ParserVersionConst }

type rawEvent struct {
	Type      string    `json:"type"`
	SessionID string    `json:"sessionId"`
	Timestamp time.Time `json:"timestamp"`
	// Cwd and GitBranch are set by Claude Code on non-trivial events and
	// provide authoritative workspace path and branch name without any
	// lossy decode from the project directory name encoding.
	Cwd       string `json:"cwd,omitempty"`
	GitBranch string `json:"gitBranch,omitempty"`
	Message   struct {
		Model   string `json:"model,omitempty"`
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name,omitempty"`
			Input struct {
				OldString string `json:"old_string,omitempty"`
				NewString string `json:"new_string,omitempty"`
				Command   string `json:"command,omitempty"`
			} `json:"input,omitempty"`
			IsError bool `json:"is_error,omitempty"`
		} `json:"content,omitempty"`
		Usage struct {
			InputTokens              int64 `json:"input_tokens,omitempty"`
			OutputTokens             int64 `json:"output_tokens,omitempty"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
		} `json:"usage,omitempty"`
	} `json:"message,omitempty"`
}

var (
	commitRegex       = regexp.MustCompile(`(?m)^git\s+commit\b`)
	prRegex           = regexp.MustCompile(`(?m)^gh\s+pr\s+create\b`)
	invalidModelKeyRe = regexp.MustCompile(`[^A-Za-z0-9._/\-]`)
)

// sanitizeModelKey replaces characters not allowed by the server's models_used
// key regex (/^[A-Za-z0-9._/-]+$/) with underscores, so values like
// "<synthetic>" are stored as "_synthetic_" rather than rejected.
func sanitizeModelKey(s string) string {
	return invalidModelKeyRe.ReplaceAllString(s, "_")
}

// decodeProjectDir converts a Claude Code encoded project directory name
// (where "/" is replaced by "-") back to an approximate workspace path.
// The result may be wrong for directories that contain real hyphens; git will
// fail silently in that case (graceful-failure contract).
func decodeProjectDir(encoded string) string {
	// The encoded name starts with "-" representing the leading "/" of an
	// absolute path. Replace all "-" with "/" to recover the path.
	return strings.ReplaceAll(encoded, "-", "/")
}

func (p *Parser) Aggregate(window parsers.TimeWindow) ([]parsers.DayAggregate, error) {
	if _, err := os.Stat(p.rootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	type dayBucket struct {
		toolInvocations           int
		successfulToolInvocations int
		linesAdded, linesRemoved  int
		commits, pullRequests     int
		models                    map[string]int
		toolsByName               map[string]int // NEW
		inputTokens               int64
		outputTokens              int64
		cacheReadTokens           int64
		cacheCreationTokens       int64
	}
	byDate := map[string]*dayBucket{}
	sessionDates := map[string]map[string]struct{ minTS, maxTS time.Time }{}

	var jsonlPaths []string
	_ = filepath.WalkDir(p.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") {
			jsonlPaths = append(jsonlPaths, path)
		}
		return nil
	})

	for _, file := range jsonlPaths {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			var ev rawEvent
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Timestamp.IsZero() {
				continue
			}
			if ev.Timestamp.Before(window.From) || !ev.Timestamp.Before(window.To) {
				continue
			}
			dateStr := ev.Timestamp.UTC().Format("2006-01-02")
			b := byDate[dateStr]
			if b == nil {
				b = &dayBucket{models: map[string]int{}, toolsByName: map[string]int{}}
				byDate[dateStr] = b
			}

			if ev.SessionID != "" {
				if sessionDates[dateStr] == nil {
					sessionDates[dateStr] = map[string]struct{ minTS, maxTS time.Time }{}
				}
				sd := sessionDates[dateStr][ev.SessionID]
				if sd.minTS.IsZero() || ev.Timestamp.Before(sd.minTS) {
					sd.minTS = ev.Timestamp
				}
				if ev.Timestamp.After(sd.maxTS) {
					sd.maxTS = ev.Timestamp
				}
				sessionDates[dateStr][ev.SessionID] = sd
			}

			switch ev.Type {
			case "assistant":
				if ev.Message.Model != "" {
					b.models[sanitizeModelKey(ev.Message.Model)]++
				}
				b.inputTokens += ev.Message.Usage.InputTokens
				b.outputTokens += ev.Message.Usage.OutputTokens
				b.cacheReadTokens += ev.Message.Usage.CacheReadInputTokens
				b.cacheCreationTokens += ev.Message.Usage.CacheCreationInputTokens
				for _, c := range ev.Message.Content {
					if c.Type == "tool_use" {
						b.toolInvocations++
						toolName := c.Name
						if toolName != "" {
							b.toolsByName[toolName]++
						}
						if c.Name == "Edit" || c.Name == "Write" {
							if c.Input.NewString != "" {
								b.linesAdded += countLines(c.Input.NewString)
							}
							if c.Input.OldString != "" {
								b.linesRemoved += countLines(c.Input.OldString)
							}
						}
						if c.Name == "Bash" {
							if commitRegex.MatchString(c.Input.Command) {
								b.commits++
							}
							if prRegex.MatchString(c.Input.Command) {
								b.pullRequests++
							}
						}
					}
				}
			case "user":
				for _, c := range ev.Message.Content {
					if c.Type == "tool_result" && !c.IsError {
						b.successfulToolInvocations++
					}
				}
			}
		}
		f.Close()
	}

	var out []parsers.DayAggregate
	for dateStr, b := range byDate {
		var totalMin int
		for _, sd := range sessionDates[dateStr] {
			diff := sd.maxTS.Sub(sd.minTS)
			if diff > 0 {
				totalMin += int(diff.Minutes())
			}
		}
		sessionCount := len(sessionDates[dateStr])
		d, _ := time.Parse("2006-01-02", dateStr)

		agg := parsers.DayAggregate{
			Date:         d,
			DateString:   dateStr,
			SessionCount: sessionCount,
			ModelsUsed:   b.models,
			MetricProvenance: map[string]string{
				"lines_method":           "edit_tool_input_estimate",
				"elapsed_method":         "max_minus_min_per_session",
				"successful_tool_method": "tool_result_no_error",
			},
			CostSource: "unknown",
		}
		if totalMin > 0 {
			agg.TotalElapsedMinutes = intPtr(totalMin)
		}
		if b.toolInvocations > 0 {
			agg.ToolInvocations = intPtr(b.toolInvocations)
		}
		if b.successfulToolInvocations > 0 {
			agg.SuccessfulToolInvocations = intPtr(b.successfulToolInvocations)
		}
		if b.linesAdded > 0 {
			agg.LinesAdded = intPtr(b.linesAdded)
		}
		if b.linesRemoved > 0 {
			agg.LinesRemoved = intPtr(b.linesRemoved)
		}
		if b.commits > 0 {
			agg.Commits = intPtr(b.commits)
		}
		if b.pullRequests > 0 {
			agg.PullRequests = intPtr(b.pullRequests)
		}
		if len(b.toolsByName) > 0 {
			agg.ToolCallsByName = b.toolsByName
		}
		if b.inputTokens > 0 {
			v := b.inputTokens
			agg.InputTokens = &v
		}
		if b.outputTokens > 0 {
			v := b.outputTokens
			agg.OutputTokens = &v
		}
		if b.cacheReadTokens > 0 {
			v := b.cacheReadTokens
			agg.CacheReadTokens = &v
		}
		if b.cacheCreationTokens > 0 {
			v := b.cacheCreationTokens
			agg.CacheCreationTokens = &v
		}
		out = append(out, agg)
	}
	return out, nil
}

// AggregateSessions emits one SessionSummary per Claude Code session ID seen
// in the window. Token sums, model mix, tool counts (by category) are scoped
// to events with that session ID. Session_date is the UTC date of the first
// event in that session.
//
// When EnableGitHints is true, the workspace path is decoded from the project
// directory name and git hints are extracted for each session's time window.
func (p *Parser) AggregateSessions(window parsers.TimeWindow) ([]parsers.SessionSummary, error) {
	if _, err := os.Stat(p.rootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	type sessionBucket struct {
		sessionKey          string
		startedAt           time.Time
		endedAt             time.Time
		models              map[string]int
		toolCounts          map[string]int
		successfulTools     int
		inputTokens         int64
		outputTokens        int64
		cacheReadTokens     int64
		cacheCreationTokens int64
		// projectDir is the encoded project directory name (direct child of rootDir).
		// Used to decode the workspace path for git hints (fallback only).
		projectDir string
		// cwd and gitBranch are captured from JSONL event fields. They are
		// authoritative and preferred over the decoded projectDir path.
		// Last-seen non-empty value wins (Claude Code can change cwd mid-session).
		cwd       string
		gitBranch string
	}
	bySession := map[string]*sessionBucket{}

	var jsonlPaths []string
	_ = filepath.WalkDir(p.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") {
			jsonlPaths = append(jsonlPaths, path)
		}
		return nil
	})

	for _, file := range jsonlPaths {
		// The project directory is the immediate child of rootDir.
		// filepath.Rel gives us the relative path; the first segment is the
		// encoded workspace name.
		relPath, _ := filepath.Rel(p.rootDir, file)
		parts := strings.SplitN(relPath, string(filepath.Separator), 2)
		projectDir := ""
		if len(parts) > 0 {
			projectDir = parts[0]
		}

		f, err := os.Open(file)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			var ev rawEvent
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Timestamp.IsZero() {
				continue
			}
			if ev.Timestamp.Before(window.From) || !ev.Timestamp.Before(window.To) {
				continue
			}
			if ev.SessionID == "" {
				continue
			}
			b := bySession[ev.SessionID]
			if b == nil {
				b = &sessionBucket{
					sessionKey: ev.SessionID,
					startedAt:  ev.Timestamp,
					endedAt:    ev.Timestamp,
					models:     map[string]int{},
					toolCounts: map[string]int{},
					projectDir: projectDir,
				}
				bySession[ev.SessionID] = b
			}
			if ev.Timestamp.Before(b.startedAt) {
				b.startedAt = ev.Timestamp
			}
			if ev.Timestamp.After(b.endedAt) {
				b.endedAt = ev.Timestamp
			}

			// Update cwd and gitBranch: last-seen non-empty value wins.
			if ev.Cwd != "" {
				b.cwd = ev.Cwd
			}
			if ev.GitBranch != "" {
				b.gitBranch = ev.GitBranch
			}

			switch ev.Type {
			case "assistant":
				if ev.Message.Model != "" {
					b.models[sanitizeModelKey(ev.Message.Model)]++
				}
				b.inputTokens += ev.Message.Usage.InputTokens
				b.outputTokens += ev.Message.Usage.OutputTokens
				b.cacheReadTokens += ev.Message.Usage.CacheReadInputTokens
				b.cacheCreationTokens += ev.Message.Usage.CacheCreationInputTokens
				for _, c := range ev.Message.Content {
					if c.Type == "tool_use" && c.Name != "" {
						b.toolCounts[categorize(c.Name)]++
					}
				}
			case "user":
				for _, c := range ev.Message.Content {
					if c.Type == "tool_result" && !c.IsError {
						b.successfulTools++
					}
				}
			}
		}
		f.Close()
	}

	out := make([]parsers.SessionSummary, 0, len(bySession))
	for _, b := range bySession {
		elapsed := int(b.endedAt.Sub(b.startedAt).Minutes())
		s := parsers.SessionSummary{
			SessionKey:     b.sessionKey,
			SessionDate:    time.Date(b.startedAt.Year(), b.startedAt.Month(), b.startedAt.Day(), 0, 0, 0, 0, time.UTC),
			StartedAt:      b.startedAt,
			EndedAt:        b.endedAt,
			ElapsedMinutes: elapsed,
			ModelsUsed:     b.models,
			ToolCounts:     b.toolCounts,
		}
		if b.successfulTools > 0 {
			s.SuccessfulToolInvocations = intPtr(b.successfulTools)
		}
		if b.inputTokens > 0 {
			v := b.inputTokens
			s.InputTokens = &v
		}
		if b.outputTokens > 0 {
			v := b.outputTokens
			s.OutputTokens = &v
		}
		if b.cacheReadTokens > 0 {
			v := b.cacheReadTokens
			s.CacheReadTokens = &v
		}
		if b.cacheCreationTokens > 0 {
			v := b.cacheCreationTokens
			s.CacheCreationTokens = &v
		}

		// Git hints (opt-in via EnableGitHints).
		if p.enableGitHints {
			// Prefer the authoritative cwd from JSONL events; fall back to
			// the lossy decoded project directory name for older JSONL files.
			workspacePath := b.cwd
			if workspacePath == "" && b.projectDir != "" {
				workspacePath = decodeProjectDir(b.projectDir)
			}
			if workspacePath != "" {
				h := githints.Extract(workspacePath, b.startedAt, b.endedAt)
				s.RepoRemoteHash = h.RepoRemoteHash
				// Prefer gitBranch from JSONL events over the shell-out result,
				// but only when the JSONL value is non-empty.
				if b.gitBranch != "" {
					s.BranchName = b.gitBranch
				} else {
					s.BranchName = h.BranchName
				}
				s.CommitSHAs = h.CommitSHAs
			}
		}

		out = append(out, s)
	}
	return out, nil
}

func intPtr(i int) *int { return &i }

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}
