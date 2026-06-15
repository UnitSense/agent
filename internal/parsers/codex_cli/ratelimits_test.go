package codex_cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

// Two token_count emissions in one rollout; the later one must win because
// rate_limits is a point-in-time gauge, not a sum.
func TestLatestRateLimit(t *testing.T) {
	dir := t.TempDir()
	older := `{"timestamp":"2026-06-02T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":2.0,"window_minutes":300,"resets_at":1780447688},"secondary":{"used_percent":0.0,"window_minutes":10080,"resets_at":1781034488},"plan_type":"plus","rate_limit_reached_type":null}}}`
	newer := `{"timestamp":"2026-06-02T14:48:33Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":42.5,"window_minutes":300,"resets_at":1780460000},"secondary":{"used_percent":11.0,"window_minutes":10080,"resets_at":1781034488},"plan_type":"pro","rate_limit_reached_type":null}}}`
	if err := os.WriteFile(filepath.Join(dir, "rollout-test.jsonl"), []byte(older+"\n"+newer+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewParser(dir)
	w := parsers.TimeWindow{From: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)}
	snap, err := p.LatestRateLimit(w)
	if err != nil {
		t.Fatalf("LatestRateLimit: %v", err)
	}
	if snap == nil {
		t.Fatal("expected a snapshot, got nil")
	}
	if snap.PlanType != "pro" {
		t.Errorf("PlanType = %q, want pro (from latest emission)", snap.PlanType)
	}
	if snap.Primary == nil || snap.Primary.UsedPercent != 42.5 || snap.Primary.WindowMinutes != 300 {
		t.Errorf("Primary = %+v, want used=42.5 window=300", snap.Primary)
	}
	if snap.Secondary == nil || snap.Secondary.UsedPercent != 11.0 || snap.Secondary.WindowMinutes != 10080 {
		t.Errorf("Secondary = %+v, want used=11.0 window=10080", snap.Secondary)
	}
	if snap.Primary.ResetsAt.IsZero() {
		t.Error("Primary.ResetsAt not parsed from resets_at")
	}
	if got := snap.CapturedAt.UTC().Format(time.RFC3339); got != "2026-06-02T14:48:33Z" {
		t.Errorf("CapturedAt = %s, want the later emission timestamp", got)
	}
}

// No rate_limits emissions in the window -> (nil, nil), not an error.
func TestLatestRateLimitNoData(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "rollout-empty.jsonl"),
		[]byte(`{"timestamp":"2026-06-02T10:00:00Z","type":"event_msg","payload":{"type":"agent_message"}}`+"\n"), 0644)
	p := NewParser(dir)
	w := parsers.TimeWindow{From: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)}
	snap, err := p.LatestRateLimit(w)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if snap != nil {
		t.Errorf("expected nil snapshot, got %+v", snap)
	}
}

// Missing root dir is benign: (nil, nil).
func TestLatestRateLimitMissingDir(t *testing.T) {
	p := NewParser("/tmp/codex-does-not-exist-zzz")
	snap, err := p.LatestRateLimit(parsers.TimeWindow{From: time.Now().Add(-time.Hour), To: time.Now()})
	if err != nil || snap != nil {
		t.Errorf("want (nil, nil), got (%v, %v)", snap, err)
	}
}
