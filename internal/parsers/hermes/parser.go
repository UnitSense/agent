// Package hermes reads Hermes Agent's local state database
// (<HERMES_HOME>/state.db) and aggregates it into the shapes the rest of
// the agent already posts for Claude Code and Codex CLI.
//
// Unlike those two tools, Hermes does not emit one JSONL file per session —
// it keeps one central SQLite database (WAL mode) at the root of its home
// directory, with a clean relational schema (confirmed by hand against a
// real local install, not assumed from docs): a `sessions` table carrying
// per-session tokens/cost/model/timing, and a `session_model_usage` table
// breaking the same figures down per model and per task within a session
// (Hermes' own auxiliary calls, e.g. auto session-title generation, show up
// there as their own row rather than being folded into the user-facing
// turn).
//
// Privacy contract (same as claude_code/codex_cli): this parser reads only
// aggregate/metadata columns. Hermes' `messages` table stores full raw
// conversation text locally — this package must never query it, and must
// never read the `messages` table at all.
package hermes

import (
	"database/sql"
	"errors"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"github.com/UnitSense/agent/internal/parsers"
	"github.com/UnitSense/agent/internal/parsers/githints"
)

const ParserVersionConst = "hermes-parser-0.1.0"

// Parser reads Hermes Agent's state.db. dbPath is the full path to that
// file, e.g. "<HERMES_HOME>/state.db" ("%LOCALAPPDATA%/hermes/state.db" on
// Windows, "~/.hermes/state.db" on macOS/Linux).
type Parser struct {
	dbPath         string
	enableGitHints bool
}

func NewParser(dbPath string) *Parser {
	return &Parser{dbPath: dbPath}
}

// NewParserWithOptions creates a parser with optional git hints support,
// matching the claude_code/codex_cli constructor shape.
func NewParserWithOptions(dbPath string, enableGitHints bool) *Parser {
	return &Parser{dbPath: dbPath, enableGitHints: enableGitHints}
}

func (p *Parser) Provider() string      { return "agent_hermes" }
func (p *Parser) Tool() string          { return "hermes" }
func (p *Parser) ParserVersion() string { return ParserVersionConst }

// open opens state.db read-only for a concurrent reader: Hermes itself may
// hold the database open (WAL mode is designed for exactly this — a live
// writer and a separate reader do not block each other). Returns (nil, nil)
// when the file does not exist yet, matching the other two parsers'
// "nothing to read" contract rather than erroring.
func (p *Parser) open() (*sql.DB, error) {
	if _, err := os.Stat(p.dbPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	db, err := sql.Open("sqlite", "file:"+p.dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	return db, nil
}

// bestCost picks the cost figure and source label for one session, driven by
// Hermes' own cost_status value -- read directly from its source
// (agent/usage_pricing.py: CostStatus = Literal["actual", "estimated",
// "included", "unknown"]), not inferred from column nullability.
//
//   - "actual":    a real billed figure the provider confirmed. ->
//     provider_reported.
//   - "estimated": Hermes priced the call itself from its local
//     model-pricing cache (models_dev_cache.json, confirmed by hand). ->
//     estimated.
//   - "included":  the call was covered by an existing subscription's
//     included quota -- amount_usd is a real, correct $0 (Hermes' own
//     source sets it to exactly zero here, distinct from "we calculated
//     $0"), because there was no separate provider invoice for this call.
//     -> derived_from_seat, this codebase's existing vocabulary for
//     "paid via a seat/subscription, not this request" (matches how
//     Claude Code/Codex's own seat-derived costs are labeled).
//   - anything else (including "unknown" or unset): -> unknown, no figure
//     reported rather than guessed.
func bestCost(actualCostUSD, estimatedCostUSD sql.NullFloat64, costStatus sql.NullString) (*float64, string) {
	status := ""
	if costStatus.Valid {
		status = costStatus.String
	}
	switch status {
	case "actual":
		if actualCostUSD.Valid {
			v := actualCostUSD.Float64
			return &v, "provider_reported"
		}
	case "estimated":
		if estimatedCostUSD.Valid {
			v := estimatedCostUSD.Float64
			return &v, "estimated"
		}
	case "included":
		// Real $0 -- subscription covered it, no separate provider charge.
		v := 0.0
		return &v, "derived_from_seat"
	}
	return nil, "unknown"
}

// costSourceRank orders cost_source strength for picking a single label when
// rolling several sessions' costs up into one day (see bucket.bestCostSourceRank).
// provider_reported and derived_from_seat are both real, confirmed bases
// (a billed figure, or a known-$0 subscription-covered call) -- ranked above
// estimated (Hermes' own calculation) and unknown (nothing usable).
func costSourceRank(source string) int {
	switch source {
	case "provider_reported":
		return 3
	case "derived_from_seat":
		return 2
	case "estimated":
		return 1
	default:
		return 0
	}
}

func rankToCostSource(rank int) string {
	switch rank {
	case 3:
		return "provider_reported"
	case 2:
		return "derived_from_seat"
	case 1:
		return "estimated"
	default:
		return "unknown"
	}
}

// epochToTime converts Hermes' REAL (float Unix-seconds) timestamp columns.
func epochToTime(sec float64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	whole := int64(sec)
	frac := sec - float64(whole)
	return time.Unix(whole, int64(frac*1e9)).UTC()
}

// sessionRow mirrors the columns this parser reads from the `sessions`
// table. Deliberately excludes system_prompt and anything from `messages`.
type sessionRow struct {
	id               string
	startedAt        float64
	endedAt          sql.NullFloat64
	model            sql.NullString
	toolCallCount    int
	inputTokens      int64
	outputTokens     int64
	cacheReadTokens  int64
	cacheWriteTokens int64
	reasoningTokens  int64
	cwd              sql.NullString
	estimatedCostUSD sql.NullFloat64
	actualCostUSD    sql.NullFloat64
	costStatus       sql.NullString
	// parentSessionID is non-empty when this session is a subagent (e.g.
	// spawned by /review or delegate_task) -- confirmed by hand against a
	// real subagent session, linked back to the exact session that created
	// it via a self-referencing foreign key in Hermes' own schema.
	parentSessionID sql.NullString
	title           sql.NullString
}

const sessionCols = `id, started_at, ended_at, model, tool_call_count,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
	cwd, estimated_cost_usd, actual_cost_usd, cost_status, parent_session_id, title`

func scanSession(rows *sql.Rows) (sessionRow, error) {
	var s sessionRow
	err := rows.Scan(&s.id, &s.startedAt, &s.endedAt, &s.model, &s.toolCallCount,
		&s.inputTokens, &s.outputTokens, &s.cacheReadTokens, &s.cacheWriteTokens, &s.reasoningTokens,
		&s.cwd, &s.estimatedCostUSD, &s.actualCostUSD, &s.costStatus, &s.parentSessionID, &s.title)
	return s, err
}

// querySessions returns every sessions row whose started_at falls in
// [window.From, window.To), the same half-open convention used elsewhere
// in this repo.
func (p *Parser) querySessions(db *sql.DB, window parsers.TimeWindow) ([]sessionRow, error) {
	rows, err := db.Query(
		`SELECT `+sessionCols+` FROM sessions
		 WHERE started_at >= ? AND started_at < ?
		 ORDER BY started_at ASC`,
		float64(window.From.Unix()), float64(window.To.Unix()),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []sessionRow
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// modelUsageRow mirrors one row of `session_model_usage` -- Hermes' own
// per-model, per-task breakdown within a session (e.g. the main turn via one
// provider, plus an auto-routed title-generation call via another, possibly
// different, provider -- confirmed by hand: a real session showed a main-turn
// row and a separate title_generation row with its own cost). Hermes' own
// analytics (agent/insights.py) treats this table as authoritative for
// token/cost breakdown whenever it has rows for a session, falling back to
// the single `sessions` row only for sessions that predate this table --
// this parser mirrors that same precedence rather than inventing its own.
type modelUsageRow struct {
	sessionID        string
	model            string
	apiCallCount     int64
	inputTokens      int64
	outputTokens     int64
	cacheReadTokens  int64
	cacheWriteTokens int64
	reasoningTokens  int64
	estimatedCostUSD float64
	actualCostUSD    float64
	costStatus       sql.NullString
}

const modelUsageCols = `u.session_id, u.model, u.api_call_count,
	u.input_tokens, u.output_tokens, u.cache_read_tokens, u.cache_write_tokens, u.reasoning_tokens,
	u.estimated_cost_usd, u.actual_cost_usd, u.cost_status`

// queryModelUsage returns session_model_usage rows for every session whose
// started_at falls in the window, grouped by session_id. The table itself
// has no started_at column, so the window filter goes through a join to
// `sessions`.
func (p *Parser) queryModelUsage(db *sql.DB, window parsers.TimeWindow) (map[string][]modelUsageRow, error) {
	rows, err := db.Query(
		`SELECT `+modelUsageCols+`
		 FROM session_model_usage u
		 JOIN sessions s ON s.id = u.session_id
		 WHERE s.started_at >= ? AND s.started_at < ?`,
		float64(window.From.Unix()), float64(window.To.Unix()),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]modelUsageRow{}
	for rows.Next() {
		var m modelUsageRow
		if err := rows.Scan(&m.sessionID, &m.model, &m.apiCallCount,
			&m.inputTokens, &m.outputTokens, &m.cacheReadTokens, &m.cacheWriteTokens, &m.reasoningTokens,
			&m.estimatedCostUSD, &m.actualCostUSD, &m.costStatus); err != nil {
			return nil, err
		}
		out[m.sessionID] = append(out[m.sessionID], m)
	}
	return out, rows.Err()
}

// sessionBreakdown is the model/token/cost picture for one session, after
// applying the session_model_usage-preferred / sessions-row-fallback
// precedence described on modelUsageRow above.
type sessionBreakdown struct {
	models           map[string]int
	inputTokens      int64
	outputTokens     int64 // reasoning tokens already folded in, matching codex_cli convention
	cacheReadTokens  int64
	cacheWriteTokens int64
	costUSD          *float64
	costSource       string
}

// computeBreakdown prefers usageRows (this session's session_model_usage
// rows, which can be zero, one, or several -- several is exactly the
// auto-routed case: a main-turn row plus one or more auxiliary-task rows,
// each potentially a different model/provider). Falls back to the single
// sessionRow's own columns only when usageRows is empty.
func computeBreakdown(s sessionRow, usageRows []modelUsageRow) sessionBreakdown {
	if len(usageRows) == 0 {
		models := map[string]int{}
		if s.model.Valid && s.model.String != "" {
			models[s.model.String] = 1
		}
		cost, source := bestCost(s.actualCostUSD, s.estimatedCostUSD, s.costStatus)
		return sessionBreakdown{
			models:           models,
			inputTokens:      s.inputTokens,
			outputTokens:     s.outputTokens + s.reasoningTokens,
			cacheReadTokens:  s.cacheReadTokens,
			cacheWriteTokens: s.cacheWriteTokens,
			costUSD:          cost,
			costSource:       source,
		}
	}

	models := map[string]int{}
	var inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64
	var costUSD float64
	var haveCost bool
	var bestRank int
	for _, u := range usageRows {
		if u.model != "" {
			models[u.model] += int(u.apiCallCount)
		}
		inputTokens += u.inputTokens
		outputTokens += u.outputTokens + u.reasoningTokens
		cacheReadTokens += u.cacheReadTokens
		cacheWriteTokens += u.cacheWriteTokens
		// Reuse bestCost per-row, same as the single-session fallback above --
		// one row's "unknown"/unset cost_status (common on auxiliary rows,
		// confirmed by hand: a real title-generation row had cost_status NULL)
		// contributes 0 toward the sum rather than being treated as a real $0.
		cost, source := bestCost(
			sql.NullFloat64{Float64: u.actualCostUSD, Valid: true},
			sql.NullFloat64{Float64: u.estimatedCostUSD, Valid: true},
			u.costStatus,
		)
		if cost != nil {
			costUSD += *cost
			haveCost = true
			if r := costSourceRank(source); r > bestRank {
				bestRank = r
			}
		}
	}
	var costPtr *float64
	source := "unknown"
	if haveCost {
		c := costUSD
		costPtr = &c
		source = rankToCostSource(bestRank)
	}
	return sessionBreakdown{
		models: models, inputTokens: inputTokens, outputTokens: outputTokens,
		cacheReadTokens: cacheReadTokens, cacheWriteTokens: cacheWriteTokens,
		costUSD: costPtr, costSource: source,
	}
}

func (p *Parser) Aggregate(window parsers.TimeWindow) ([]parsers.DayAggregate, error) {
	db, err := p.open()
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, nil
	}
	defer db.Close()

	sessions, err := p.querySessions(db, window)
	if err != nil {
		return nil, err
	}
	usageBySession, err := p.queryModelUsage(db, window)
	if err != nil {
		return nil, err
	}

	type bucket struct {
		sessionIDs       map[string]struct{}
		models           map[string]int
		toolInvocations  int
		inputTokens      int64
		outputTokens     int64
		cacheReadTokens  int64
		cacheWriteTokens int64
		elapsedMinutes   int
		costUSD          float64
		haveCost         bool
		// bestCostSourceRank tracks the strongest cost_source seen this day:
		// provider_reported (2) > estimated (1) > unknown (0), so a day with
		// any real billed session is labeled provider_reported even if other
		// sessions that day were only estimated.
		bestCostSourceRank int
	}
	newBucket := func() *bucket {
		return &bucket{sessionIDs: map[string]struct{}{}, models: map[string]int{}}
	}
	byDate := map[string]*bucket{}

	for _, s := range sessions {
		startedAt := epochToTime(s.startedAt)
		dateStr := startedAt.Format("2006-01-02")
		b := byDate[dateStr]
		if b == nil {
			b = newBucket()
			byDate[dateStr] = b
		}
		b.sessionIDs[s.id] = struct{}{}
		// computeBreakdown prefers this session's session_model_usage rows
		// (main turn plus any auto-routed auxiliary-task rows) over the
		// single sessions-row columns -- see computeBreakdown's own doc.
		bd := computeBreakdown(s, usageBySession[s.id])
		for model, calls := range bd.models {
			b.models[model] += calls
		}
		b.toolInvocations += s.toolCallCount
		b.inputTokens += bd.inputTokens
		b.outputTokens += bd.outputTokens
		b.cacheReadTokens += bd.cacheReadTokens
		b.cacheWriteTokens += bd.cacheWriteTokens
		if bd.costUSD != nil {
			b.costUSD += *bd.costUSD
			b.haveCost = true
			if rank := costSourceRank(bd.costSource); rank > b.bestCostSourceRank {
				b.bestCostSourceRank = rank
			}
		}
		if s.endedAt.Valid {
			d := epochToTime(s.endedAt.Float64).Sub(startedAt)
			if d > 0 {
				b.elapsedMinutes += int(d.Minutes())
			}
		}
	}

	var out []parsers.DayAggregate
	for dateStr, b := range byDate {
		dt, _ := time.Parse("2006-01-02", dateStr)
		agg := parsers.DayAggregate{
			Date:         dt,
			DateString:   dateStr,
			SessionCount: len(b.sessionIDs),
			ModelsUsed:   b.models,
			// Summed from Hermes' own per-session cost (hermes-reported, not
			// estimated by this agent). See bestCost/costSourceRank above.
			CostSource: rankToCostSource(b.bestCostSourceRank),
			MetricProvenance: map[string]string{
				"token_source": "hermes session_model_usage when present, else sessions table (hermes-reported, not estimated by this parser)",
				"cost_method":  "summed per-session from session_model_usage (main turn + auto-routed auxiliary tasks) when present, else sessions.actual_cost_usd/estimated_cost_usd",
			},
		}
		if b.haveCost {
			c := b.costUSD
			agg.EstimatedCostUSD = &c
		}
		if b.elapsedMinutes > 0 {
			m := b.elapsedMinutes
			agg.TotalElapsedMinutes = &m
		}
		if b.toolInvocations > 0 {
			t := b.toolInvocations
			agg.ToolInvocations = &t
		}
		if b.inputTokens > 0 {
			v := b.inputTokens
			agg.InputTokens = &v
		}
		if b.outputTokens > 0 {
			v := b.outputTokens
			agg.OutputTokens = &v
		}
		if b.cacheWriteTokens > 0 {
			v := b.cacheWriteTokens
			agg.CacheCreationTokens = &v
		}
		if b.cacheReadTokens > 0 {
			v := b.cacheReadTokens
			agg.CacheReadTokens = &v
		}
		out = append(out, agg)
	}
	return out, nil
}

// AggregateSessions emits one SessionSummary per Hermes session row.
func (p *Parser) AggregateSessions(window parsers.TimeWindow) ([]parsers.SessionSummary, error) {
	db, err := p.open()
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, nil
	}
	defer db.Close()

	sessions, err := p.querySessions(db, window)
	if err != nil {
		return nil, err
	}
	usageBySession, err := p.queryModelUsage(db, window)
	if err != nil {
		return nil, err
	}

	out := make([]parsers.SessionSummary, 0, len(sessions))
	for _, s := range sessions {
		startedAt := epochToTime(s.startedAt)
		endedAt := startedAt
		if s.endedAt.Valid {
			endedAt = epochToTime(s.endedAt.Float64)
		}
		elapsed := int(endedAt.Sub(startedAt).Minutes())

		// computeBreakdown prefers this session's session_model_usage rows
		// (main turn plus any auto-routed auxiliary-task rows, e.g. an
		// auto-generated title via a different model/provider) over the
		// single sessions-row columns -- see computeBreakdown's own doc.
		bd := computeBreakdown(s, usageBySession[s.id])

		summary := parsers.SessionSummary{
			SessionKey:     s.id,
			SessionDate:    time.Date(startedAt.Year(), startedAt.Month(), startedAt.Day(), 0, 0, 0, 0, time.UTC),
			StartedAt:      startedAt,
			EndedAt:        endedAt,
			ElapsedMinutes: elapsed,
			ModelsUsed:     bd.models,
			// Hermes does not categorize tool calls by name/category at the
			// session-row level (that detail lives in `messages`, which this
			// parser does not read per the privacy contract above). Only the
			// count is available here, and ToolCounts has no "uncategorized"
			// bucket in the shared schema, so it stays empty rather than
			// inventing one.
			ToolCounts: map[string]int{},
		}
		if s.title.Valid && s.title.String != "" {
			summary.Title = s.title.String
		}
		if s.parentSessionID.Valid && s.parentSessionID.String != "" {
			summary.ParentSessionKey = s.parentSessionID.String
		}
		if bd.inputTokens > 0 {
			v := bd.inputTokens
			summary.InputTokens = &v
		}
		if bd.outputTokens > 0 {
			v := bd.outputTokens
			summary.OutputTokens = &v
		}
		if bd.cacheReadTokens > 0 {
			v := bd.cacheReadTokens
			summary.CacheReadTokens = &v
		}
		if bd.cacheWriteTokens > 0 {
			v := bd.cacheWriteTokens
			summary.CacheCreationTokens = &v
		}
		summary.EstimatedCostUSD, summary.CostSource = bd.costUSD, bd.costSource

		if p.enableGitHints && s.cwd.Valid && s.cwd.String != "" {
			h := githints.Extract(s.cwd.String, startedAt, endedAt)
			summary.RepoRemoteHash = h.RepoRemoteHash
			summary.BranchName = h.BranchName
			summary.CommitSHAs = h.CommitSHAs
		}

		out = append(out, summary)
	}
	return out, nil
}
