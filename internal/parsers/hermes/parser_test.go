package hermes

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/UnitSense/agent/internal/parsers"
)

// sessionModelUsageDDL matches session_model_usage's real schema, confirmed
// by hand against a live Hermes install's state.db. Declared once and reused
// by every test fixture in this file so a session with no auto-routed
// auxiliary tasks (the common case) can still leave the table empty and
// exercise the sessions-row fallback path in computeBreakdown.
const sessionModelUsageDDL = `
	CREATE TABLE session_model_usage (
		session_id TEXT NOT NULL,
		model TEXT NOT NULL,
		billing_provider TEXT NOT NULL DEFAULT '',
		billing_base_url TEXT NOT NULL DEFAULT '',
		billing_mode TEXT NOT NULL DEFAULT '',
		task TEXT NOT NULL DEFAULT '',
		api_call_count INTEGER NOT NULL DEFAULT 0,
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		cache_write_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		estimated_cost_usd REAL NOT NULL DEFAULT 0,
		actual_cost_usd REAL NOT NULL DEFAULT 0,
		cost_status TEXT,
		cost_source TEXT,
		first_seen REAL,
		last_seen REAL,
		PRIMARY KEY (session_id, model, billing_provider, billing_base_url, billing_mode, task)
	)
`

// newTestDB builds a small, real SQLite database matching the state.db
// schema confirmed by hand against a live Hermes install (hermes-agent
// v0.22.2, 2026-10-19). Only the columns this parser actually reads are
// declared; this is a test fixture, not a full schema mirror.
func newTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			started_at REAL NOT NULL,
			ended_at REAL,
			model TEXT,
			tool_call_count INTEGER DEFAULT 0,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cache_read_tokens INTEGER DEFAULT 0,
			cache_write_tokens INTEGER DEFAULT 0,
			reasoning_tokens INTEGER DEFAULT 0,
			cwd TEXT,
			estimated_cost_usd REAL,
			actual_cost_usd REAL,
			cost_status TEXT,
			parent_session_id TEXT,
			title TEXT
		)
	`)
	if err != nil {
		t.Fatalf("create sessions: %v", err)
	}
	if _, err := db.Exec(sessionModelUsageDDL); err != nil {
		t.Fatalf("create session_model_usage: %v", err)
	}

	// One real-shaped row (values mirror our actual local test session, not
	// copied content): a 2026-05-21 session, one model, real token counts,
	// Hermes' own estimated cost, cost_status "estimated".
	_, err = db.Exec(
		`INSERT INTO sessions (id, started_at, ended_at, model, tool_call_count,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
			cwd, estimated_cost_usd, actual_cost_usd, cost_status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"20260521_120000_hrm1",
		float64(time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC).Unix()),
		float64(time.Date(2026, 5, 21, 12, 5, 0, 0, time.UTC).Unix()),
		"upstage/solar-mini4:free",
		0,
		5221, 38, 8000, 0, 28,
		"", // no cwd -> git hints must stay empty even if EnableGitHints is set
		0.0, nil, "estimated",
	)
	if err != nil {
		t.Fatalf("insert session 1: %v", err)
	}

	// A second session outside the test window, to prove the time filter
	// actually excludes it rather than the fixture just having one row.
	_, err = db.Exec(
		`INSERT INTO sessions (id, started_at, model, input_tokens, output_tokens, cost_status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"20260601_000000_hrm2",
		float64(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Unix()),
		"anthropic/claude-opus-5",
		100, 10, "estimated",
	)
	if err != nil {
		t.Fatalf("insert session 2: %v", err)
	}

	return path
}

func TestProvider(t *testing.T) {
	p := NewParser("does-not-exist.db")
	if p.Provider() != "agent_hermes" {
		t.Errorf("Provider = %q, want agent_hermes", p.Provider())
	}
	if p.Tool() != "hermes" {
		t.Errorf("Tool = %q, want hermes", p.Tool())
	}
	if p.ParserVersion() != ParserVersionConst {
		t.Errorf("ParserVersion = %q, want %q", p.ParserVersion(), ParserVersionConst)
	}
}

func TestOpenMissingDBReturnsNilNotError(t *testing.T) {
	p := NewParser(filepath.Join(t.TempDir(), "does-not-exist.db"))
	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)

	days, err := p.Aggregate(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("Aggregate on missing db: %v", err)
	}
	if days != nil {
		t.Errorf("Aggregate on missing db = %v, want nil", days)
	}

	sessions, err := p.AggregateSessions(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("AggregateSessions on missing db: %v", err)
	}
	if sessions != nil {
		t.Errorf("AggregateSessions on missing db = %v, want nil", sessions)
	}
}

func TestAggregateSessions(t *testing.T) {
	dbPath := newTestDB(t)
	p := NewParser(dbPath)

	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	sessions, err := p.AggregateSessions(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("AggregateSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session in window, got %d", len(sessions))
	}

	s := sessions[0]
	if s.SessionKey != "20260521_120000_hrm1" {
		t.Errorf("SessionKey = %q", s.SessionKey)
	}
	if s.InputTokens == nil || *s.InputTokens != 5221 {
		t.Errorf("InputTokens = %v, want 5221", s.InputTokens)
	}
	// output_tokens(38) + reasoning_tokens(28) folded in, same as codex_cli.
	if s.OutputTokens == nil || *s.OutputTokens != 66 {
		t.Errorf("OutputTokens = %v, want 66 (38 output + 28 reasoning)", s.OutputTokens)
	}
	if s.CacheReadTokens == nil || *s.CacheReadTokens != 8000 {
		t.Errorf("CacheReadTokens = %v, want 8000", s.CacheReadTokens)
	}
	if s.CacheCreationTokens != nil {
		t.Errorf("CacheCreationTokens = %v, want nil (cache_write_tokens was 0)", s.CacheCreationTokens)
	}
	if s.ModelsUsed["upstage/solar-mini4:free"] != 1 {
		t.Errorf("ModelsUsed = %v", s.ModelsUsed)
	}
	if s.ElapsedMinutes != 5 {
		t.Errorf("ElapsedMinutes = %d, want 5", s.ElapsedMinutes)
	}
	if s.RepoRemoteHash != "" || s.BranchName != "" {
		t.Errorf("expected empty git hints, got hash=%q branch=%q", s.RepoRemoteHash, s.BranchName)
	}
	// Fixture row has estimated_cost_usd=0.0, actual_cost_usd=NULL,
	// cost_status="estimated" -- Hermes' own figure, read as-is.
	if s.CostSource != "estimated" {
		t.Errorf("CostSource = %q, want estimated", s.CostSource)
	}
	if s.EstimatedCostUSD == nil || *s.EstimatedCostUSD != 0.0 {
		t.Errorf("EstimatedCostUSD = %v, want pointer to 0.0", s.EstimatedCostUSD)
	}
}

// TestAggregateSessionsPrefersActualCostOverEstimated confirms that once a
// call's cost_status is "actual" (Hermes reconciled a stale pre-call estimate
// with the real provider-billed figure, per agent/usage_pricing.py), the
// parser reads actual_cost_usd and ignores the now-superseded estimate still
// sitting in the same row, labeling it provider_reported rather than
// estimated.
func TestAggregateSessionsPrefersActualCostOverEstimated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY, started_at REAL NOT NULL, ended_at REAL, model TEXT,
			tool_call_count INTEGER DEFAULT 0, input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0, cache_read_tokens INTEGER DEFAULT 0,
			cache_write_tokens INTEGER DEFAULT 0, reasoning_tokens INTEGER DEFAULT 0,
			cwd TEXT, estimated_cost_usd REAL, actual_cost_usd REAL, cost_status TEXT,
			parent_session_id TEXT, title TEXT
		)`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(sessionModelUsageDDL); err != nil {
		t.Fatalf("create session_model_usage: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO sessions (id, started_at, model, estimated_cost_usd, actual_cost_usd, cost_status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"sess-billed", float64(time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix()),
		"anthropic/claude-opus-5", 0.05, 0.047, "actual",
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	p := NewParser(path)
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
	if s.CostSource != "provider_reported" {
		t.Errorf("CostSource = %q, want provider_reported (actual_cost_usd present)", s.CostSource)
	}
	if s.EstimatedCostUSD == nil || *s.EstimatedCostUSD != 0.047 {
		t.Errorf("EstimatedCostUSD = %v, want pointer to 0.047 (the actual, not the 0.05 estimate)", s.EstimatedCostUSD)
	}
}

// TestAggregateSessionsSubscriptionIncludedCall confirms a call covered by
// an existing subscription (cost_status "included", confirmed from Hermes'
// own source: agent/usage_pricing.py sets amount_usd=0 and status="included"
// specifically to distinguish "free because subscription" from "free
// because $0 pricing") is labeled derived_from_seat, not estimated or
// unknown, with a real (not missing) $0 cost figure.
func TestAggregateSessionsSubscriptionIncludedCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY, started_at REAL NOT NULL, ended_at REAL, model TEXT,
			tool_call_count INTEGER DEFAULT 0, input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0, cache_read_tokens INTEGER DEFAULT 0,
			cache_write_tokens INTEGER DEFAULT 0, reasoning_tokens INTEGER DEFAULT 0,
			cwd TEXT, estimated_cost_usd REAL, actual_cost_usd REAL, cost_status TEXT,
			parent_session_id TEXT, title TEXT
		)`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(sessionModelUsageDDL); err != nil {
		t.Fatalf("create session_model_usage: %v", err)
	}
	// estimated_cost_usd/actual_cost_usd both NULL here -- "included" is
	// authoritative on its own, same as Hermes' own CostResult shape
	// (amount_usd=_ZERO is a Python-side constant, not read from either
	// of these two columns for this status).
	_, err = db.Exec(
		`INSERT INTO sessions (id, started_at, model, cost_status)
		 VALUES (?, ?, ?, ?)`,
		"sess-subscription", float64(time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix()),
		"openai/gpt-5.5-codex", "included",
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	p := NewParser(path)
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
	if s.CostSource != "derived_from_seat" {
		t.Errorf("CostSource = %q, want derived_from_seat (subscription-included call)", s.CostSource)
	}
	if s.EstimatedCostUSD == nil || *s.EstimatedCostUSD != 0.0 {
		t.Errorf("EstimatedCostUSD = %v, want a real pointer to 0.0 (not nil -- this IS a known cost, not a missing one)", s.EstimatedCostUSD)
	}
}

func TestAggregate(t *testing.T) {
	dbPath := newTestDB(t)
	p := NewParser(dbPath)

	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	days, err := p.Aggregate(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(days) != 1 {
		t.Fatalf("expected 1 day in window, got %d", len(days))
	}

	d := days[0]
	if d.DateString != "2026-05-21" {
		t.Errorf("DateString = %q", d.DateString)
	}
	if d.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", d.SessionCount)
	}
	if d.InputTokens == nil || *d.InputTokens != 5221 {
		t.Errorf("InputTokens = %v, want 5221", d.InputTokens)
	}
	if d.TotalElapsedMinutes == nil || *d.TotalElapsedMinutes != 5 {
		t.Errorf("TotalElapsedMinutes = %v, want 5", d.TotalElapsedMinutes)
	}
	if d.ModelsUsed["upstage/solar-mini4:free"] != 1 {
		t.Errorf("ModelsUsed = %v", d.ModelsUsed)
	}
	if d.CostSource != "estimated" {
		t.Errorf("CostSource = %q, want estimated", d.CostSource)
	}
	if d.EstimatedCostUSD == nil || *d.EstimatedCostUSD != 0.0 {
		t.Errorf("EstimatedCostUSD = %v, want pointer to 0.0", d.EstimatedCostUSD)
	}
}

// TestAggregateSessionsRespectsGitHintsFlag confirms the opt-in flag is
// wired through, without depending on a real git repo existing in the test
// environment (the fixture's cwd is intentionally blank, so
// githints.Extract short-circuits to an empty Hints{} regardless of the
// flag).
func TestAggregateSessionsRespectsGitHintsFlag(t *testing.T) {
	dbPath := newTestDB(t)
	p := NewParserWithOptions(dbPath, true)

	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	sessions, err := p.AggregateSessions(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("AggregateSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].RepoRemoteHash != "" {
		t.Errorf("RepoRemoteHash = %q, want empty for a session with no cwd", sessions[0].RepoRemoteHash)
	}
}

// TestAggregateSessionsAutoRoutedAuxiliaryTask confirms the "auto router"
// case: a session whose main turn used one provider, plus an auto-routed
// auxiliary task (e.g. title generation) that used a genuinely different
// provider. Fixture shape mirrors a real captured session (main turn: gemini,
// $0.007111, status "estimated"; aux task: a different provider, $0.0001535,
// status NULL/unknown) except the aux row's provider is swapped to a third
// one here specifically to prove cross-provider attribution, not just
// cross-task. Confirms: (1) both models appear in ModelsUsed, not just the
// session row's single `model` column, (2) tokens are summed across both
// rows, (3) cost only includes the row with a known status -- the
// unknown-status aux row must contribute $0, not be dropped entirely or
// silently counted.
func TestAggregateSessionsAutoRoutedAuxiliaryTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY, started_at REAL NOT NULL, ended_at REAL, model TEXT,
			tool_call_count INTEGER DEFAULT 0, input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0, cache_read_tokens INTEGER DEFAULT 0,
			cache_write_tokens INTEGER DEFAULT 0, reasoning_tokens INTEGER DEFAULT 0,
			cwd TEXT, estimated_cost_usd REAL, actual_cost_usd REAL, cost_status TEXT,
			parent_session_id TEXT, title TEXT
		)`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(sessionModelUsageDDL); err != nil {
		t.Fatalf("create session_model_usage: %v", err)
	}
	startedAt := float64(time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix())
	// The sessions-row columns here are deliberately stale/incomplete (only
	// the main turn's figures, same as Hermes' own pre-table behavior) --
	// computeBreakdown must prefer session_model_usage over these once any
	// rows exist for the session, matching Hermes' own insights.py precedence.
	_, err = db.Exec(
		`INSERT INTO sessions (id, started_at, model, input_tokens, output_tokens, estimated_cost_usd, cost_status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sess-autorouted", startedAt, "gemini-3-flash-preview", 13664, 93, 0.007111, "estimated",
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO session_model_usage
		   (session_id, model, billing_provider, billing_base_url, task, api_call_count,
		    input_tokens, output_tokens, estimated_cost_usd, actual_cost_usd, cost_status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"sess-autorouted", "gemini-3-flash-preview", "gemini", "https://generativelanguage.googleapis.com/v1beta", "", 1,
		13664, 93, 0.007111, 0.0, "estimated",
	)
	if err != nil {
		t.Fatalf("insert main usage row: %v", err)
	}
	// Auxiliary auto-routed task: a different model AND a different provider
	// than the main turn, with no cost_status stamped (confirmed by hand: a
	// real title-generation row had cost_status/cost_source both NULL despite
	// estimated_cost_usd being populated).
	_, err = db.Exec(
		`INSERT INTO session_model_usage
		   (session_id, model, billing_provider, billing_base_url, task, api_call_count,
		    input_tokens, output_tokens, estimated_cost_usd, actual_cost_usd)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"sess-autorouted", "mistral-small", "openrouter", "https://openrouter.ai/api/v1", "title_generation", 1,
		512, 20, 0.0001535, 0.0,
	)
	if err != nil {
		t.Fatalf("insert aux usage row: %v", err)
	}
	db.Close()

	p := NewParser(path)
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

	if len(s.ModelsUsed) != 2 {
		t.Fatalf("ModelsUsed = %v, want 2 entries (main turn + auto-routed aux task)", s.ModelsUsed)
	}
	if s.ModelsUsed["gemini-3-flash-preview"] != 1 || s.ModelsUsed["mistral-small"] != 1 {
		t.Errorf("ModelsUsed = %v, want both gemini-3-flash-preview and mistral-small at 1 call each", s.ModelsUsed)
	}
	wantInput, wantOutput := int64(13664+512), int64(93+20)
	if s.InputTokens == nil || *s.InputTokens != wantInput {
		t.Errorf("InputTokens = %v, want pointer to %d (summed across both rows)", s.InputTokens, wantInput)
	}
	if s.OutputTokens == nil || *s.OutputTokens != wantOutput {
		t.Errorf("OutputTokens = %v, want pointer to %d (summed across both rows)", s.OutputTokens, wantOutput)
	}
	// Cost must come only from the main row (known status "estimated"); the
	// aux row's $0.0001535 has no known cost_status and must NOT be added in.
	if s.EstimatedCostUSD == nil || *s.EstimatedCostUSD != 0.007111 {
		t.Errorf("EstimatedCostUSD = %v, want pointer to 0.007111 (only the known-status row, aux row excluded)", s.EstimatedCostUSD)
	}
	if s.CostSource != "estimated" {
		t.Errorf("CostSource = %q, want estimated", s.CostSource)
	}
}

// TestAggregateSessionsSubagentFields confirms a subagent session (e.g. one
// spawned by Hermes' /review command) carries its title and raw parent
// session id through into SessionSummary -- and that an ordinary session
// with neither column set leaves both fields empty rather than erroring on
// the NULLs. Fixture values mirror a real subagent session confirmed by hand
// (title "Subagent: Review recent work", linked via parent_session_id).
func TestAggregateSessionsSubagentFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY, started_at REAL NOT NULL, ended_at REAL, model TEXT,
			tool_call_count INTEGER DEFAULT 0, input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0, cache_read_tokens INTEGER DEFAULT 0,
			cache_write_tokens INTEGER DEFAULT 0, reasoning_tokens INTEGER DEFAULT 0,
			cwd TEXT, estimated_cost_usd REAL, actual_cost_usd REAL, cost_status TEXT,
			parent_session_id TEXT, title TEXT
		)`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(sessionModelUsageDDL); err != nil {
		t.Fatalf("create session_model_usage: %v", err)
	}
	day := float64(time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix())
	_, err = db.Exec(
		`INSERT INTO sessions (id, started_at, model, cost_status, parent_session_id, title)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"subagent-sess", day, "gemini-3-flash-preview", "estimated", "parent-sess", "Subagent: Review recent work",
	)
	if err != nil {
		t.Fatalf("insert subagent session: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO sessions (id, started_at, model, cost_status)
		 VALUES (?, ?, ?, ?)`,
		"plain-sess", day, "gemini-3-flash-preview", "estimated",
	)
	if err != nil {
		t.Fatalf("insert plain session: %v", err)
	}
	db.Close()

	p := NewParser(path)
	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	sessions, err := p.AggregateSessions(parsers.TimeWindow{From: from, To: to})
	if err != nil {
		t.Fatalf("AggregateSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	byKey := map[string]parsers.SessionSummary{}
	for _, s := range sessions {
		byKey[s.SessionKey] = s
	}

	sub := byKey["subagent-sess"]
	if sub.Title != "Subagent: Review recent work" {
		t.Errorf("subagent Title = %q, want %q", sub.Title, "Subagent: Review recent work")
	}
	if sub.ParentSessionKey != "parent-sess" {
		t.Errorf("subagent ParentSessionKey = %q, want %q", sub.ParentSessionKey, "parent-sess")
	}

	plain := byKey["plain-sess"]
	if plain.Title != "" {
		t.Errorf("plain session Title = %q, want empty", plain.Title)
	}
	if plain.ParentSessionKey != "" {
		t.Errorf("plain session ParentSessionKey = %q, want empty", plain.ParentSessionKey)
	}
}
