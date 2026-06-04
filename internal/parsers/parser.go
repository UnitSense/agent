package parsers

import "time"

type TimeWindow struct {
	From time.Time
	To   time.Time
}

type DayAggregate struct {
	Date                      time.Time
	DateString                string
	SessionCount              int
	TotalElapsedMinutes       *int
	LinesAdded                *int
	LinesRemoved              *int
	Commits                   *int
	PullRequests              *int
	ToolInvocations           *int
	SuccessfulToolInvocations *int
	ModelsUsed                map[string]int
	ToolCallsByName           map[string]int
	EstimatedCostUSD          *float64
	CostSource                string
	MetricProvenance          map[string]string
	// Real-token sums when the upstream JSONL carries usage telemetry.
	// nil when the parser couldn't extract them (older sessions, Codex
	// events without an info block, etc).
	InputTokens         *int64
	OutputTokens        *int64
	CacheReadTokens     *int64
	CacheCreationTokens *int64
}

// SessionSummary is the per-session shape posted alongside daily aggregates.
// Daily aggregates remain authoritative for time-series rollups; sessions
// enrich them with inspectable start/end timestamps, repo/branch hints,
// and finer-grained token + tool data.
//
// Privacy contract:
//   - SessionKey is the raw source-session id; the agent hashes it before
//     posting (sha256 of machine_id + SessionKey + SessionDate).
//   - No prompts, responses, raw tool input/output, file content, diffs,
//     or raw command bodies appear in this struct.
//   - ToolCounts uses category names (read/edit/write/shell/search/other),
//     not raw tool/command names where possible.
type SessionSummary struct {
	SessionKey                string             // raw source-session id; hashed before post
	SessionDate               time.Time          // UTC date the session occurred on
	StartedAt                 time.Time
	EndedAt                   time.Time
	ElapsedMinutes            int
	ModelsUsed                map[string]int
	InputTokens               *int64
	OutputTokens              *int64
	CacheReadTokens           *int64
	CacheCreationTokens       *int64
	ToolCounts                map[string]int     // by category
	SuccessfulToolInvocations *int
	// Local git hints (filled by F2 agent v0.5.0; nil/empty until then).
	RepoRemoteHash            string
	BranchName                string
	CommitSHAs                []string
}

type Parser interface {
	Provider() string      // "agent_claude_code"
	Tool() string          // "claude_code"
	ParserVersion() string // "claude-code-parser-0.6.0"
	Aggregate(window TimeWindow) ([]DayAggregate, error)
	AggregateSessions(window TimeWindow) ([]SessionSummary, error)
}
