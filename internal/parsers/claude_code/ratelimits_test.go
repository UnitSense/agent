package claude_code

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

// writeCapture points quotaFilePath at a temp file containing q and returns a
// cleanup that restores the original.
func writeCapture(t *testing.T, q CapturedQuota) func() {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unitsense-quota.json")
	data, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	orig := quotaFilePath
	quotaFilePath = func() string { return path }
	return func() { quotaFilePath = orig }
}

// withTier points claudeConfigPath at a fixture giving the named plan tier.
func withTier(t *testing.T, tier string) func() {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude.json")
	_ = os.WriteFile(path, []byte(`{"oauthAccount":{"organizationRateLimitTier":"`+tier+`"}}`), 0o644)
	orig := claudeConfigPath
	claudeConfigPath = func() string { return path }
	return func() { claudeConfigPath = orig }
}

func TestLatestRateLimitFromCapture(t *testing.T) {
	now := time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)
	fiveReset := now.Add(90 * time.Minute)
	weekReset := now.Add(6 * 24 * time.Hour)
	defer writeCapture(t, CapturedQuota{
		CapturedAt: now.Add(-2 * time.Minute), // fresh
		FiveHour:   &CapturedWindow{UsedPercent: 29, ResetsAt: fiveReset},
		SevenDay:   &CapturedWindow{UsedPercent: 4, ResetsAt: weekReset},
	})()
	defer withTier(t, "default_claude_max_5x")()

	p := NewParser(t.TempDir())
	snap, err := p.LatestRateLimit(parsers.TimeWindow{To: now})
	if err != nil {
		t.Fatalf("LatestRateLimit: %v", err)
	}
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap.Estimated {
		t.Error("captured snapshot must be Estimated=false (authoritative)")
	}
	if snap.PlanType != "default_claude_max_5x" {
		t.Errorf("PlanType = %q, want default_claude_max_5x", snap.PlanType)
	}
	if snap.Primary == nil || snap.Primary.UsedPercent != 29 {
		t.Errorf("Primary = %+v, want used 29%%", snap.Primary)
	}
	if snap.Primary.WindowMinutes != fiveHourMinutes {
		t.Errorf("Primary.WindowMinutes = %d, want 300", snap.Primary.WindowMinutes)
	}
	if !snap.Primary.ResetsAt.Equal(fiveReset) {
		t.Errorf("Primary.ResetsAt = %s, want %s", snap.Primary.ResetsAt, fiveReset)
	}
	if snap.Secondary == nil || snap.Secondary.UsedPercent != 4 {
		t.Errorf("Secondary = %+v, want used 4%%", snap.Secondary)
	}
	if snap.Secondary.WindowMinutes != weeklyMinutes {
		t.Errorf("Secondary.WindowMinutes = %d, want 10080", snap.Secondary.WindowMinutes)
	}
}

// A capture older than the 5-hour window has rolled over for the 5h period but
// is still valid weekly — Primary drops, Secondary stays.
func TestLatestRateLimitFiveHourStale(t *testing.T) {
	now := time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)
	defer writeCapture(t, CapturedQuota{
		CapturedAt: now.Add(-6 * time.Hour), // older than 5h, younger than 7d
		FiveHour:   &CapturedWindow{UsedPercent: 80, ResetsAt: now.Add(-time.Hour)},
		SevenDay:   &CapturedWindow{UsedPercent: 12, ResetsAt: now.Add(5 * 24 * time.Hour)},
	})()

	p := NewParser(t.TempDir())
	snap, err := p.LatestRateLimit(parsers.TimeWindow{To: now})
	if err != nil {
		t.Fatalf("LatestRateLimit: %v", err)
	}
	if snap == nil || snap.Primary != nil {
		t.Errorf("expected stale 5h window dropped, got %+v", snap)
	}
	if snap.Secondary == nil || snap.Secondary.UsedPercent != 12 {
		t.Errorf("expected weekly retained, got %+v", snap.Secondary)
	}
}

// A capture older than the weekly window is entirely stale → nil.
func TestLatestRateLimitFullyStale(t *testing.T) {
	now := time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)
	defer writeCapture(t, CapturedQuota{
		CapturedAt: now.Add(-8 * 24 * time.Hour),
		FiveHour:   &CapturedWindow{UsedPercent: 50, ResetsAt: now},
		SevenDay:   &CapturedWindow{UsedPercent: 90, ResetsAt: now},
	})()

	p := NewParser(t.TempDir())
	snap, err := p.LatestRateLimit(parsers.TimeWindow{To: now})
	if err != nil {
		t.Fatalf("LatestRateLimit: %v", err)
	}
	if snap != nil {
		t.Errorf("expected nil for fully-stale capture, got %+v", snap)
	}
}

func TestLatestRateLimitNoCaptureFile(t *testing.T) {
	orig := quotaFilePath
	quotaFilePath = func() string { return filepath.Join(t.TempDir(), "does-not-exist.json") }
	defer func() { quotaFilePath = orig }()

	p := NewParser(t.TempDir())
	snap, err := p.LatestRateLimit(parsers.TimeWindow{To: time.Now()})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if snap != nil {
		t.Errorf("expected nil snapshot when no capture file, got %+v", snap)
	}
}
