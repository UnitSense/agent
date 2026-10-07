package cli

import (
	"path/filepath"
	"testing"

	"github.com/UnitSense/agent/internal/config"
	"github.com/google/uuid"
)

func TestResolveMachineIDReusesSavedID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	want := uuid.New()
	cfg := &config.Config{
		ServerURL:   "http://localhost:3000",
		DeviceToken: "ust_dev_test",
		MachineID:   want,
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := resolveMachineID(path)
	if got != want {
		t.Fatalf("resolveMachineID() = %s, want saved id %s", got, want)
	}
}

func TestResolveMachineIDGeneratesNewWhenNoConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.toml")

	got := resolveMachineID(path)
	if got == uuid.Nil {
		t.Fatalf("resolveMachineID() returned nil uuid when no config exists")
	}
}

func TestResolveMachineIDGeneratesNewWhenSavedIDIsNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	cfg := &config.Config{
		ServerURL:   "http://localhost:3000",
		DeviceToken: "ust_dev_test",
		// MachineID left as uuid.Nil, mimicking a pre-this-fix config.
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := resolveMachineID(path)
	if got == uuid.Nil {
		t.Fatalf("resolveMachineID() returned nil uuid for a config with nil MachineID")
	}
}
