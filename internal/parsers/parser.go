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

// RateLimitWindow is provider-reported utilization for one rolling window
// (e.g. Codex's 5-hour "primary" or 7-day "secondary"). UsedPercent and
// ResetsAt are read directly from the provider — not estimated.
type RateLimitWindow struct {
	UsedPercent   float64   // 0..100, as reported by the provider
	WindowMinutes int       // 300 = rolling 5h, 10080 = weekly
	ResetsAt      time.Time // when this window's usage resets
}

// RateLimitSnapshot is a point-in-time view of a developer's subscription
// quota, captured at CapturedAt. It is authoritative (provider-reported) where
// the local session files persist it (Codex today); the Claude path will fill
// this in from a statusline hook or estimate from tokens (see the spec).
type RateLimitSnapshot struct {
	Source     string           // parser Provider(), e.g. "agent_codex_cli"
	PlanType   string           // provider plan tier, e.g. "plus", "pro"
	CapturedAt time.Time        // timestamp of the emission this came from
	Primary    *RateLimitWindow // rolling window (nil if absent)
	Secondary  *RateLimitWindow // weekly window (nil if absent)
}

// RateLimitReader is an OPTIONAL capability. Parsers whose source files persist
// provider-reported rate-limit utilization implement it; the run loop
// type-asserts and skips parsers that don't (e.g. Claude Code today). This
// keeps the change additive — no existing Parser is forced to implement it.
type RateLimitReader interface {
	// LatestRateLimit returns the most recent rate-limit snapshot within the
	// window, or (nil, nil) when none is available (no recent activity / the
	// source doesn't carry it).
	LatestRateLimit(window TimeWindow) (*RateLimitSnapshot, error)
}
