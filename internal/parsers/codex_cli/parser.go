package codex_cli

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

const ParserVersionConst = "codex-cli-parser-0.7.0" // 0.7.0: capture rate_limits (subscription quota)

// Codex tool-name categorization. Codex CLI emits a small set of canonical
// names so the mapping is short.
var codexToolCategory = map[string]string{
	"exec_command": "shell",
	"apply_patch":  "edit",
	"read_file":    "read",
	"list_dir":     "read",
	"grep":         "search",
	"web_fetch":    "fetch",
}

func categorize(name string) string {
	if c, ok := codexToolCategory[name]; ok {
		return c
	}
	return "other"
}

var (
	commitRegex = regexp.MustCompile(`\bgit\s+commit\b`)
	prRegex     = regexp.MustCompile(`\bgh\s+pr\s+create\b`)
	modelKeyRe  = regexp.MustCompile(`[^A-Za-z0-9._/\-]+`)
)

// Parser reads Codex CLI JSONL session files from ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
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

func (p *Parser) Provider() string      { return "agent_codex_cli" }
func (p *Parser) Tool() string          { return "codex_cli" }
func (p *Parser) ParserVersion() string { return ParserVersionConst }

// rawEvent is the top-level wrapper for every line in a Codex JSONL file.
type rawEvent struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID  string `json:"id"`
	CWD string `json:"cwd"`
}

type turnContextPayload struct {
	Model string `json:"model"`
}

type eventMsgPayload struct {
	Type     string   `json:"type"`
	Command  []string `json:"command,omitempty"`
	ExitCode *int     `json:"exit_code,omitempty"`
	Success  *bool    `json:"success,omitempty"`
	// token_count payloads carry an info block with per-turn usage.
	// info is null on rate-limit-only emissions, so it's optional.
	Info *struct {
		LastTokenUsage *struct {
			InputTokens           int64 `json:"input_tokens"`
			CachedInputTokens     int64 `json:"cached_input_tokens"`
			OutputTokens          int64 `json:"output_tokens"`
			ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
		} `json:"last_token_usage,omitempty"`
	} `json:"info,omitempty"`
	// rate_limits is the provider-reported subscription quota, present on
	// token_count emissions (incl. the rate-limit-only ones where info is null).
	// Captured by LatestRateLimit (see ratelimits.go).
	RateLimits *rateLimitsRaw `json:"rate_limits,omitempty"`
}

type responseItemPayload struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

func sanitizeModelKey(raw string) string {
	return modelKeyRe.ReplaceAllString(raw, "_")
}

func (p *Parser) Aggregate(window parsers.TimeWindow) ([]parsers.DayAggregate, error) {
	if _, err := os.Stat(p.rootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	type sessionTimes struct {
		min, max time.Time
	}

	type bucket struct {
		// session id -> time range (for elapsed minutes)
		sessionTimes map[string]*sessionTimes
		// session ids seen (for session count)
		sessionIDs map[string]struct{}

		toolInvocations           int
		successfulToolInvocations int
		commits                   int
		pullRequests              int
		models                    map[string]int
		toolsByName               map[string]int // NEW
		inputTokens               int64
		outputTokens              int64
		cacheReadTokens           int64
	}

	newBucket := func() *bucket {
		return &bucket{
			sessionTimes: map[string]*sessionTimes{},
			sessionIDs:   map[string]struct{}{},
			models:       map[string]int{},
			toolsByName:  map[string]int{},
		}
	}

	byDate := map[string]*bucket{}

	getBucket := func(dateStr string) *bucket {
		b := byDate[dateStr]
		if b == nil {
			b = newBucket()
			byDate[dateStr] = b
		}
		return b
	}

	// Track current session id per file (session_meta is first event in each file)
	_ = filepath.WalkDir(p.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		var currentSessionID string

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)

		for scanner.Scan() {
			var ev rawEvent
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Timestamp.IsZero() || ev.Timestamp.Before(window.From) || !ev.Timestamp.Before(window.To) {
				continue
			}

			dateStr := ev.Timestamp.UTC().Format("2006-01-02")
			b := getBucket(dateStr)

			// Track min/max for elapsed time using the current session id
			if currentSessionID != "" {
				st := b.sessionTimes[currentSessionID]
				if st == nil {
					st = &sessionTimes{}
					b.sessionTimes[currentSessionID] = st
				}
				if st.min.IsZero() || ev.Timestamp.Before(st.min) {
					st.min = ev.Timestamp
				}
				if ev.Timestamp.After(st.max) {
					st.max = ev.Timestamp
				}
			}

			switch ev.Type {
			case "session_meta":
				var sm sessionMetaPayload
				if err := json.Unmarshal(ev.Payload, &sm); err == nil && sm.ID != "" {
					currentSessionID = sm.ID
					b.sessionIDs[sm.ID] = struct{}{}
					// Initialize session times for this date
					if b.sessionTimes[sm.ID] == nil {
						b.sessionTimes[sm.ID] = &sessionTimes{}
					}
					st := b.sessionTimes[sm.ID]
					if st.min.IsZero() || ev.Timestamp.Before(st.min) {
						st.min = ev.Timestamp
					}
					if ev.Timestamp.After(st.max) {
						st.max = ev.Timestamp
					}
				}

			case "turn_context":
				var tc turnContextPayload
				if err := json.Unmarshal(ev.Payload, &tc); err == nil && tc.Model != "" {
					key := sanitizeModelKey(tc.Model)
					b.models[key]++
				}

			case "response_item":
				var ri responseItemPayload
				if err := json.Unmarshal(ev.Payload, &ri); err == nil {
					if ri.Type == "function_call" || ri.Type == "custom_tool_call" {
						b.toolInvocations++
						if ri.Name != "" {
							b.toolsByName[ri.Name]++
						}
					}
				}

			case "event_msg":
				var em eventMsgPayload
				if err := json.Unmarshal(ev.Payload, &em); err != nil {
					continue
				}
				switch em.Type {
				case "token_count":
					if em.Info != nil && em.Info.LastTokenUsage != nil {
						u := em.Info.LastTokenUsage
						b.inputTokens += u.InputTokens
						b.cacheReadTokens += u.CachedInputTokens
						// Reasoning tokens are billed as output; fold them in.
						b.outputTokens += u.OutputTokens + u.ReasoningOutputTokens
					}

				case "exec_command_end":
					exitOK := em.ExitCode != nil && *em.ExitCode == 0
					joined := strings.Join(em.Command, " ")

					if exitOK {
						b.successfulToolInvocations++
					}
					// Count commits and PRs only for successful commands (exit_code == 0)
					if exitOK {
						if commitRegex.MatchString(joined) {
							b.commits++
						}
						if prRegex.MatchString(joined) {
							b.pullRequests++
						}
					}

				case "patch_apply_end":
					if em.Success != nil && *em.Success {
						b.successfulToolInvocations++
					}
				}
			}
		}
		return nil
	})

	var out []parsers.DayAggregate
	for dateStr, b := range byDate {
		var elapsedMin int
		for _, st := range b.sessionTimes {
			if !st.min.IsZero() && !st.max.IsZero() {
				d := st.max.Sub(st.min)
				if d > 0 {
					elapsedMin += int(d.Minutes())
				}
			}
		}

		dt, _ := time.Parse("2006-01-02", dateStr)
		agg := parsers.DayAggregate{
			Date:         dt,
			DateString:   dateStr,
			SessionCount: len(b.sessionIDs),
			ModelsUsed:   b.models,
			MetricProvenance: map[string]string{
				"elapsed_method":         "max_minus_min_per_session",
				"successful_tool_method": "exec_command_end.exit_code==0 + patch_apply_end.success==true",
				"commit_method":          "exec_command_end exit_code==0 matching git commit regex",
				"pr_method":              "exec_command_end exit_code==0 matching gh pr create regex",
			},
			CostSource: "unknown",
		}
		if elapsedMin > 0 {
			m := elapsedMin
			agg.TotalElapsedMinutes = &m
		}
		if b.toolInvocations > 0 {
			t := b.toolInvocations
			agg.ToolInvocations = &t
		}
		if b.successfulToolInvocations > 0 {
			st := b.successfulToolInvocations
			agg.SuccessfulToolInvocations = &st
		}
		if b.commits > 0 {
			c := b.commits
			agg.Commits = &c
		}
		if b.pullRequests > 0 {
			pr := b.pullRequests
			agg.PullRequests = &pr
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
		// Codex CLI does not emit cache_creation tokens (only cached_input_tokens
		// which we map to CacheReadTokens). CacheCreationTokens stays nil.
		out = append(out, agg)
	}
	return out, nil
}

// AggregateSessions emits one SessionSummary per Codex CLI session. Each
// rollout-*.jsonl file is normally one session, identified by the first
// session_meta event's `id` field.
//
// When EnableGitHints is true, the CWD from session_meta is used as the
// workspace path for git hint extraction.
func (p *Parser) AggregateSessions(window parsers.TimeWindow) ([]parsers.SessionSummary, error) {
	if _, err := os.Stat(p.rootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	type sessionBucket struct {
		sessionKey      string
		startedAt       time.Time
		endedAt         time.Time
		models          map[string]int
		toolCounts      map[string]int
		successfulTools int
		inputTokens     int64
		outputTokens    int64
		cacheReadTokens int64
		// cwd from session_meta, used for git hints
		cwd string
	}
	bySession := map[string]*sessionBucket{}

	_ = filepath.WalkDir(p.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		var currentSessionID string

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			var ev rawEvent
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Timestamp.IsZero() || ev.Timestamp.Before(window.From) || !ev.Timestamp.Before(window.To) {
				continue
			}

			if ev.Type == "session_meta" {
				var sm sessionMetaPayload
				if err := json.Unmarshal(ev.Payload, &sm); err == nil && sm.ID != "" {
					currentSessionID = sm.ID
					b := bySession[currentSessionID]
					if b == nil {
						b = &sessionBucket{
							sessionKey: currentSessionID,
							startedAt:  ev.Timestamp,
							endedAt:    ev.Timestamp,
							models:     map[string]int{},
							toolCounts: map[string]int{},
							cwd:        sm.CWD,
						}
						bySession[currentSessionID] = b
					}
				}
				continue
			}
			if currentSessionID == "" {
				continue
			}
			b := bySession[currentSessionID]
			if b == nil {
				continue
			}
			if ev.Timestamp.Before(b.startedAt) {
				b.startedAt = ev.Timestamp
			}
			if ev.Timestamp.After(b.endedAt) {
				b.endedAt = ev.Timestamp
			}

			switch ev.Type {
			case "turn_context":
				var tc turnContextPayload
				if err := json.Unmarshal(ev.Payload, &tc); err == nil && tc.Model != "" {
					b.models[sanitizeModelKey(tc.Model)]++
				}
			case "response_item":
				var ri responseItemPayload
				if err := json.Unmarshal(ev.Payload, &ri); err == nil {
					if ri.Type == "function_call" || ri.Type == "custom_tool_call" {
						if ri.Name != "" {
							b.toolCounts[categorize(ri.Name)]++
						}
					}
				}
			case "event_msg":
				var em eventMsgPayload
				if err := json.Unmarshal(ev.Payload, &em); err != nil {
					continue
				}
				switch em.Type {
				case "token_count":
					if em.Info != nil && em.Info.LastTokenUsage != nil {
						u := em.Info.LastTokenUsage
						b.inputTokens += u.InputTokens
						b.cacheReadTokens += u.CachedInputTokens
						b.outputTokens += u.OutputTokens + u.ReasoningOutputTokens
					}
				case "exec_command_end":
					if em.ExitCode != nil && *em.ExitCode == 0 {
						b.successfulTools++
					}
				case "patch_apply_end":
					if em.Success != nil && *em.Success {
						b.successfulTools++
					}
				}
			}
		}
		return nil
	})

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
			n := b.successfulTools
			s.SuccessfulToolInvocations = &n
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
		// Codex doesn't emit cache_creation; field stays nil.

		// Git hints (opt-in via EnableGitHints).
		if p.enableGitHints && b.cwd != "" {
			h := githints.Extract(b.cwd, b.startedAt, b.endedAt)
			s.RepoRemoteHash = h.RepoRemoteHash
			s.BranchName = h.BranchName
			s.CommitSHAs = h.CommitSHAs
		}

		out = append(out, s)
	}
	return out, nil
}
