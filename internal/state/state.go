package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type State struct {
	LastRunAt                string `json:"last_run_at,omitempty"`
	LastRunStatus            string `json:"last_run_status,omitempty"`
	LastRunDurationMS        int    `json:"last_run_duration_ms,omitempty"`
	LastRunEventsSent        int    `json:"last_run_events_sent,omitempty"`
	LastError                string `json:"last_error,omitempty"`
	ConsecutiveFailures      int    `json:"consecutive_failures,omitempty"`
	ScheduledIntervalMinutes int    `json:"scheduled_interval_minutes,omitempty"`

	// Outcome of the most recent self-update attempt (internal/selfupdate),
	// held here because the agent exits between scheduled ticks -- reported
	// on the *next* run's check-in, one cycle late, then cleared, the same
	// way the runlog diary reports the previous run's sync outcome.
	LastUpdateOutcome string `json:"last_update_outcome,omitempty"`
	LastUpdateDetail  string `json:"last_update_detail,omitempty"`
}

func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func Save(path string, s *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
