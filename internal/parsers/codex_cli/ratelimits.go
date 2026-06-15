package codex_cli

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

// rateLimitsRaw mirrors the `rate_limits` object Codex CLI writes onto
// token_count events in rollout-*.jsonl. Shape (observed 2026-06):
//
//	"rate_limits": {
//	  "limit_id": "codex",
//	  "primary":   {"used_percent": 2.0, "window_minutes": 300,   "resets_at": 1780447688},
//	  "secondary": {"used_percent": 0.0, "window_minutes": 10080, "resets_at": 1781034488},
//	  "plan_type": "plus",
//	  "rate_limit_reached_type": null
//	}
//
// primary = rolling 5-hour window, secondary = weekly (7-day). Values are
// provider-reported (what `codex /status` shows) — authoritative, not estimated.
type rateLimitsRaw struct {
	PlanType  string       `json:"plan_type"`
	Primary   *rlWindowRaw `json:"primary"`
	Secondary *rlWindowRaw `json:"secondary"`
}

type rlWindowRaw struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"` // unix seconds
}

func (r *rlWindowRaw) toWindow() *parsers.RateLimitWindow {
	if r == nil {
		return nil
	}
	var reset time.Time
	if r.ResetsAt > 0 {
		reset = time.Unix(r.ResetsAt, 0).UTC()
	}
	return &parsers.RateLimitWindow{
		UsedPercent:   r.UsedPercent,
		WindowMinutes: r.WindowMinutes,
		ResetsAt:      reset,
	}
}

// LatestRateLimit scans the rollout files for the most recent token_count event
// carrying a rate_limits object within the window, and returns it as a snapshot.
// Returns (nil, nil) when the tenant has no Codex activity carrying rate limits
// in the window. Implements parsers.RateLimitReader.
//
// Rationale: rate_limits is a point-in-time gauge (current % used), so only the
// freshest emission is meaningful — we keep the one with the max timestamp.
func (p *Parser) LatestRateLimit(window parsers.TimeWindow) (*parsers.RateLimitSnapshot, error) {
	if _, err := os.Stat(p.rootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	var latest *parsers.RateLimitSnapshot
	var latestTS time.Time

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
			if json.Unmarshal(scanner.Bytes(), &ev) != nil {
				continue
			}
			if ev.Type != "event_msg" {
				continue
			}
			if ev.Timestamp.IsZero() || ev.Timestamp.Before(window.From) || !ev.Timestamp.Before(window.To) {
				continue
			}
			var pl eventMsgPayload
			if json.Unmarshal(ev.Payload, &pl) != nil || pl.RateLimits == nil {
				continue
			}
			if !ev.Timestamp.After(latestTS) {
				continue
			}
			latestTS = ev.Timestamp
			latest = &parsers.RateLimitSnapshot{
				Source:     p.Provider(),
				PlanType:   pl.RateLimits.PlanType,
				CapturedAt: ev.Timestamp.UTC(),
				Primary:    pl.RateLimits.Primary.toWindow(),
				Secondary:  pl.RateLimits.Secondary.toWindow(),
			}
		}
		return nil
	})

	return latest, nil
}

// compile-time assertion that the Codex parser implements the optional capability.
var _ parsers.RateLimitReader = (*Parser)(nil)
