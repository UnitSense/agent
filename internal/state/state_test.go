package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadEmptyReturnsZero(t *testing.T) {
	dir := t.TempDir()
	st, err := Load(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.LastRunAt != "" {
		t.Fatalf("expected empty state, got %+v", st)
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := &State{
		LastRunAt:           time.Now().UTC().Format(time.RFC3339),
		LastRunStatus:       "succeeded",
		LastRunDurationMS:   1842,
		LastRunEventsSent:   2,
		ConsecutiveFailures: 0,
	}
	if err := Save(path, st); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LastRunStatus != "succeeded" || loaded.LastRunEventsSent != 2 {
		t.Fatalf("round-trip mismatch: %+v", loaded)
	}
}
