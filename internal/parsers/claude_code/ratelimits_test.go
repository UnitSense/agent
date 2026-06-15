package claude_code

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

// assistant event line with given timestamp, model, and output tokens.
func asst(ts time.Time, model string, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":%q,"usage":{"output_tokens":%d}}}`,
		ts.UTC().Format(time.RFC3339), model, out)
}

func TestLatestRateLimitEstimate(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	lines := []string{
		asst(now.Add(-2*time.Hour), "claude-sonnet-4-6", 1_000_000),    // in active 5h block
		asst(now.Add(-1*time.Hour), "claude-opus-4-8", 1_000_000),      // opus → weight 5x
		asst(now.Add(-30*time.Hour), "claude-sonnet-4-6", 2_000_000),   // in weekly, NOT in 5h block
	}
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "p.jsonl"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// point claude.json at a fixture giving the Max-5x tier + a weekly reset.
	cfgPath := filepath.Join(dir, "claude.json")
	_ = os.WriteFile(cfgPath, []byte(`{"oauthAccount":{"organizationRateLimitTier":"default_claude_max_5x"},"cachedGrowthBookFeatures":{"tengu_saffron_lattice":{"planLimitsEndDate":"2026-06-22T10:00:00Z"}}}`), 0644)
	orig := claudeConfigPath
	claudeConfigPath = func() string { return cfgPath }
	defer func() { claudeConfigPath = orig }()

	p := NewParser(dir)
	w := parsers.TimeWindow{From: now.Add(-weeklyWindow), To: now}
	snap, err := p.LatestRateLimit(w)
	if err != nil {
		t.Fatalf("LatestRateLimit: %v", err)
	}
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if !snap.Estimated {
		t.Error("Claude snapshot must be Estimated=true")
	}
	if snap.PlanType != "default_claude_max_5x" {
		t.Errorf("PlanType = %q, want default_claude_max_5x (from claude.json)", snap.PlanType)
	}
	// 5h block weighted tokens = 1M(sonnet,1x) + 1M(opus,5x) = 6M; budget 40M → 15%.
	wantPrimary := 6_000_000.0 / 40_000_000.0 * 100
	if d := snap.Primary.UsedPercent - wantPrimary; d > 0.01 || d < -0.01 {
		t.Errorf("Primary.UsedPercent = %.3f, want %.3f", snap.Primary.UsedPercent, wantPrimary)
	}
	if snap.Primary.WindowMinutes != fiveHourMinutes {
		t.Errorf("Primary.WindowMinutes = %d, want 300", snap.Primary.WindowMinutes)
	}
	// weekly weighted = 6M + 2M(sonnet,1x) = 8M; budget 300M.
	wantWeekly := 8_000_000.0 / 300_000_000.0 * 100
	if d := snap.Secondary.UsedPercent - wantWeekly; d > 0.01 || d < -0.01 {
		t.Errorf("Secondary.UsedPercent = %.3f, want %.3f", snap.Secondary.UsedPercent, wantWeekly)
	}
	// weekly reset comes from claude.json planLimitsEndDate.
	if got := snap.Secondary.ResetsAt.UTC().Format(time.RFC3339); got != "2026-06-22T10:00:00Z" {
		t.Errorf("Secondary.ResetsAt = %s, want 2026-06-22T10:00:00Z (from claude.json)", got)
	}
}

func TestLatestRateLimitNoUsage(t *testing.T) {
	dir := t.TempDir()
	p := NewParser(dir)
	snap, err := p.LatestRateLimit(parsers.TimeWindow{From: time.Now().Add(-weeklyWindow), To: time.Now()})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if snap != nil {
		t.Errorf("expected nil snapshot for empty dir, got %+v", snap)
	}
}
