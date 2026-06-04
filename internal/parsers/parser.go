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

type Parser interface {
	Provider() string      // "agent_claude_code"
	Tool() string          // "claude_code"
	ParserVersion() string // "claude-code-parser-0.3.0"
	Aggregate(window TimeWindow) ([]DayAggregate, error)
}
