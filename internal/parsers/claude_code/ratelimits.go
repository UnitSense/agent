package claude_code

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

// Claude Code does NOT persist live rate-limit utilization to disk (unlike Codex
// rollout files). So we ESTIMATE: bucket token usage into the rolling 5-hour
// block (ccusage's block algorithm) and the trailing weekly window, weight by
// model, and divide by a configured per-tier budget. Plan tier + the weekly
// reset come from ~/.claude.json. Snapshots are flagged Estimated=true.
//
// The numbers in tierBudgets / modelWeight are PLACEHOLDERS — Anthropic does not
// publish Max plan thresholds. They must be calibrated (spec open question #1);
// the plumbing here is what matters until then.

const (
	fiveHourMinutes = 300
	weeklyMinutes   = 10080
	blockDuration   = 5 * time.Hour
	weeklyWindow    = 7 * 24 * time.Hour
)

// tierBudget is the weighted-token allowance per window for a plan tier.
type tierBudget struct {
	fiveHour float64
	weekly   float64
}

// PLACEHOLDER budgets in "Sonnet-equivalent weighted tokens". Calibrate later.
var tierBudgets = map[string]tierBudget{
	"default_claude_max_5x":  {fiveHour: 40_000_000, weekly: 300_000_000},
	"default_claude_max_20x": {fiveHour: 160_000_000, weekly: 1_200_000_000},
	"default_claude_pro":     {fiveHour: 8_000_000, weekly: 60_000_000},
}

// fallbackBudget when the tier is unknown / claude.json is unreadable.
var fallbackBudget = tierBudget{fiveHour: 40_000_000, weekly: 300_000_000}

// modelWeight approximates how fast a model burns the Max budget relative to
// Sonnet (Opus is far heavier). PLACEHOLDER — calibrate.
func modelWeight(model string) float64 {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "opus"):
		return 5.0
	case strings.Contains(m, "haiku"):
		return 0.25
	default: // sonnet and unknown
		return 1.0
	}
}

// claudeConfigPath is overridable in tests; defaults to ~/.claude.json.
var claudeConfigPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// readClaudeConfig pulls the plan tier and weekly reset from ~/.claude.json.
// Returns ("", zero) when absent/unreadable — the estimate still works without it.
func readClaudeConfig() (tier string, weeklyReset time.Time) {
	path := claudeConfigPath()
	if path == "" {
		return "", time.Time{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}
	}
	var cfg struct {
		OAuthAccount struct {
			OrganizationRateLimitTier string `json:"organizationRateLimitTier"`
		} `json:"oauthAccount"`
		CachedGrowthBookFeatures struct {
			SaffronLattice struct {
				PlanLimitsEndDate string `json:"planLimitsEndDate"`
			} `json:"tengu_saffron_lattice"`
		} `json:"cachedGrowthBookFeatures"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return "", time.Time{}
	}
	tier = cfg.OAuthAccount.OrganizationRateLimitTier
	if s := cfg.CachedGrowthBookFeatures.SaffronLattice.PlanLimitsEndDate; s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			weeklyReset = t.UTC()
		}
	}
	return tier, weeklyReset
}

type usageEntry struct {
	ts       time.Time
	weighted float64
}

// LatestRateLimit estimates the developer's current 5h + weekly quota usage from
// transcript token data. Implements parsers.RateLimitReader. Returns (nil, nil)
// when there's no usage in the window.
func (p *Parser) LatestRateLimit(window parsers.TimeWindow) (*parsers.RateLimitSnapshot, error) {
	if _, err := os.Stat(p.rootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	now := window.To
	if now.IsZero() {
		now = time.Now().UTC()
	}
	weekStart := now.Add(-weeklyWindow)

	var entries []usageEntry
	_ = filepath.WalkDir(p.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			var ev rawEvent
			if json.Unmarshal(scanner.Bytes(), &ev) != nil || ev.Type != "assistant" {
				continue
			}
			if ev.Timestamp.IsZero() || ev.Timestamp.Before(weekStart) || !ev.Timestamp.Before(now) {
				continue
			}
			u := ev.Message.Usage
			total := float64(u.InputTokens + u.OutputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens)
			if total <= 0 {
				continue
			}
			entries = append(entries, usageEntry{ts: ev.Timestamp.UTC(), weighted: total * modelWeight(ev.Message.Model)})
		}
		return nil
	})

	if len(entries) == 0 {
		return nil, nil
	}

	// Weekly = sum over the trailing 7 days (already filtered above).
	var weekly float64
	for _, e := range entries {
		weekly += e.weighted
	}

	// 5h block (ccusage): start floored to the hour; a >5h gap (since block start
	// or since last entry) starts a new block. The active block = the last block
	// whose last entry is within 5h of now.
	sortByTS(entries)
	var blockStart, lastTS time.Time
	var blockWeighted float64
	for _, e := range entries {
		if blockStart.IsZero() || e.ts.Sub(blockStart) > blockDuration || e.ts.Sub(lastTS) > blockDuration {
			blockStart = e.ts.Truncate(time.Hour) // floor to hour
			blockWeighted = 0
		}
		blockWeighted += e.weighted
		lastTS = e.ts
	}
	var primaryUsed float64
	var primaryReset time.Time
	if !blockStart.IsZero() && now.Sub(lastTS) < blockDuration && now.Before(blockStart.Add(blockDuration)) {
		primaryUsed = blockWeighted
		primaryReset = blockStart.Add(blockDuration)
	} else {
		primaryReset = now // current block already elapsed/reset
	}

	tier, weeklyReset := readClaudeConfig()
	if weeklyReset.IsZero() {
		weeklyReset = now.Add(weeklyWindow)
	}
	budget, ok := tierBudgets[tier]
	if !ok {
		budget = fallbackBudget
	}

	pct := func(used, limit float64) float64 {
		if limit <= 0 {
			return 0
		}
		return used / limit * 100
	}

	return &parsers.RateLimitSnapshot{
		Source:     p.Provider(),
		PlanType:   tier,
		CapturedAt: now.UTC(),
		Estimated:  true,
		Primary:    &parsers.RateLimitWindow{UsedPercent: pct(primaryUsed, budget.fiveHour), WindowMinutes: fiveHourMinutes, ResetsAt: primaryReset},
		Secondary:  &parsers.RateLimitWindow{UsedPercent: pct(weekly, budget.weekly), WindowMinutes: weeklyMinutes, ResetsAt: weeklyReset},
	}, nil
}

// sortByTS sorts entries ascending by timestamp (small N; insertion-free stdlib).
func sortByTS(e []usageEntry) {
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && e[j-1].ts.After(e[j].ts); j-- {
			e[j-1], e[j] = e[j], e[j-1]
		}
	}
}

var _ parsers.RateLimitReader = (*Parser)(nil)
