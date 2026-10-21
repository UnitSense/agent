package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

// TestSessionsToPayloadSubagentFields confirms Title passes through as-is
// (it's a deliberately human-readable label, not a raw filesystem id) and
// ParentSessionKey gets hashed into parent_session_key_hash -- deliberately
// NOT the same formula as session_key_hash (that one folds in session_date,
// which we don't know for the parent from here), so the hash is just
// machine_id + raw id. Confirms this is deterministic by recomputing the
// same hash independently.
func TestSessionsToPayloadSubagentFields(t *testing.T) {
	machineID := "11111111-1111-1111-1111-111111111111"
	day := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	sessions := []parsers.SessionSummary{
		{
			SessionKey:       "subagent-sess",
			SessionDate:      day,
			StartedAt:        day,
			EndedAt:          day,
			Title:            "Subagent: Review recent work",
			ParentSessionKey: "parent-sess",
			ModelsUsed:       map[string]int{},
			ToolCounts:       map[string]int{},
		},
	}

	out := sessionsToPayload(sessions, machineID)
	if len(out) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out))
	}
	ev := out[0]

	if got, want := ev["title"], "Subagent: Review recent work"; got != want {
		t.Errorf("title = %v, want %v", got, want)
	}

	wantHasher := sha256.New()
	wantHasher.Write([]byte(machineID))
	wantHasher.Write([]byte(":"))
	wantHasher.Write([]byte("parent-sess"))
	wantHash := hex.EncodeToString(wantHasher.Sum(nil))
	if got := ev["parent_session_key_hash"]; got != wantHash {
		t.Errorf("parent_session_key_hash = %v, want %v", got, wantHash)
	}

	// The hash must not depend on session_date -- recomputing with the same
	// machine_id + raw parent id but a different session_date must match.
	altSessions := []parsers.SessionSummary{
		{
			SessionKey:       "subagent-sess-2",
			SessionDate:      day.AddDate(0, 0, 5),
			StartedAt:        day.AddDate(0, 0, 5),
			EndedAt:          day.AddDate(0, 0, 5),
			ParentSessionKey: "parent-sess",
			ModelsUsed:       map[string]int{},
			ToolCounts:       map[string]int{},
		},
	}
	altOut := sessionsToPayload(altSessions, machineID)
	if got := altOut[0]["parent_session_key_hash"]; got != wantHash {
		t.Errorf("parent_session_key_hash changed with session_date: got %v, want %v (stable)", got, wantHash)
	}
}

// TestSessionsToPayloadOmitsEmptySubagentFields confirms a session with no
// title/parent (Claude Code and Codex CLI today, and most Hermes sessions)
// sends neither key at all, rather than an empty string/hash -- matching
// the existing omission pattern used for every other optional field here.
func TestSessionsToPayloadOmitsEmptySubagentFields(t *testing.T) {
	day := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	sessions := []parsers.SessionSummary{
		{
			SessionKey:  "plain-sess",
			SessionDate: day,
			StartedAt:   day,
			EndedAt:     day,
			ModelsUsed:  map[string]int{},
			ToolCounts:  map[string]int{},
		},
	}
	out := sessionsToPayload(sessions, "machine-1")
	ev := out[0]
	if _, ok := ev["title"]; ok {
		t.Errorf("title present for a session with no title: %v", ev["title"])
	}
	if _, ok := ev["parent_session_key_hash"]; ok {
		t.Errorf("parent_session_key_hash present for a session with no parent: %v", ev["parent_session_key_hash"])
	}
}
