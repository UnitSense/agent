package claude_code

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

// Claude Code (v2.1.132+) hands LIVE rate-limit utilization to statusLine
// commands on stdin — rate_limits.five_hour / .seven_day, each with
// used_percentage (0..100) and resets_at (unix epoch seconds). These are the
// authoritative, provider-reported numbers shown on Claude's Settings → Usage
// page. Claude Code does NOT persist them to disk on its own, so the
// `unitsense-agent statusline` subcommand captures that payload to quotaFile and
// we read it back here. No estimation — Estimated is always false.
//
// (Earlier versions ESTIMATED usage from transcript token counts against guessed
// per-tier budgets; that was wildly inaccurate — weekly off by ~25x — so it was
// replaced with this authoritative capture.)

const (
	fiveHourMinutes = 300
	weeklyMinutes   = 10080
	fiveHourWindow  = 5 * time.Hour
	weeklyWindow    = 7 * 24 * time.Hour
)

// quotaFilePath is the location the statusline subcommand writes to and this
// parser reads from: ~/.claude/unitsense-quota.json. Overridable in tests.
var quotaFilePath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "unitsense-quota.json")
}

// QuotaFilePath exposes the capture-file location to the statusline subcommand
// so writer and reader never drift.
func QuotaFilePath() string { return quotaFilePath() }

// claudeConfigPath is overridable in tests; defaults to ~/.claude.json. Used
// only to label the snapshot with the plan tier (the percentages come from the
// captured statusLine payload, not from here).
var claudeConfigPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// readClaudeTier pulls the plan tier (e.g. default_claude_max_5x) from
// ~/.claude.json. Returns "" when absent/unreadable — the snapshot is still
// valid, just without a plan label.
func readClaudeTier() string {
	path := claudeConfigPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg struct {
		OAuthAccount struct {
			OrganizationRateLimitTier string `json:"organizationRateLimitTier"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	return cfg.OAuthAccount.OrganizationRateLimitTier
}

// CapturedWindow is one rolling window as written by the statusline subcommand.
type CapturedWindow struct {
	UsedPercent float64   `json:"used_percent"`
	ResetsAt    time.Time `json:"resets_at"`
}

// CapturedQuota is the on-disk shape of ~/.claude/unitsense-quota.json.
type CapturedQuota struct {
	CapturedAt time.Time       `json:"captured_at"`
	FiveHour   *CapturedWindow `json:"five_hour,omitempty"`
	SevenDay   *CapturedWindow `json:"seven_day,omitempty"`
}

// LatestRateLimit reads the authoritative 5h + weekly utilization captured by
// the statusline subcommand. Implements parsers.RateLimitReader. Returns
// (nil, nil) when there's no usable capture (file absent, unparseable, or so
// stale the windows have since rolled over).
func (p *Parser) LatestRateLimit(window parsers.TimeWindow) (*parsers.RateLimitSnapshot, error) {
	data, err := os.ReadFile(quotaFilePath())
	if err != nil {
		return nil, nil // not configured yet, or Claude Code hasn't rendered a statusline
	}
	var q CapturedQuota
	if json.Unmarshal(data, &q) != nil || q.CapturedAt.IsZero() {
		return nil, nil
	}

	now := window.To
	if now.IsZero() {
		now = time.Now().UTC()
	}
	age := now.Sub(q.CapturedAt)

	snap := &parsers.RateLimitSnapshot{
		Source:     p.Provider(),
		PlanType:   readClaudeTier(),
		CapturedAt: q.CapturedAt.UTC(),
		Estimated:  false,
	}
	// A window is only trustworthy while the capture is fresher than the
	// window itself. Older than that and it has rolled over since we last
	// saw it (the developer simply hasn't used Claude Code since), so the
	// reading no longer reflects the current period — drop it.
	if q.FiveHour != nil && age < fiveHourWindow {
		snap.Primary = &parsers.RateLimitWindow{
			UsedPercent:   clampPct(q.FiveHour.UsedPercent),
			WindowMinutes: fiveHourMinutes,
			ResetsAt:      q.FiveHour.ResetsAt.UTC(),
		}
	}
	if q.SevenDay != nil && age < weeklyWindow {
		snap.Secondary = &parsers.RateLimitWindow{
			UsedPercent:   clampPct(q.SevenDay.UsedPercent),
			WindowMinutes: weeklyMinutes,
			ResetsAt:      q.SevenDay.ResetsAt.UTC(),
		}
	}
	if snap.Primary == nil && snap.Secondary == nil {
		return nil, nil
	}
	return snap, nil
}

// clampPct keeps a percentage in [0,100]. The captured values are already
// 0..100, but a defensive clamp keeps payloads inside the ingest schema bounds.
func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

var _ parsers.RateLimitReader = (*Parser)(nil)
