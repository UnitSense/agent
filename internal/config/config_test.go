package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
)

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.toml")

	cfg := &Config{
		ServerURL:     "https://app.unitsense.ai",
		DeviceToken:   "ust_dev_test",
		Tenant:        "saltest",
		Email:         "alice@acme.com",
		MachineID:     uuid.New(),
		Providers:     []string{"claude_code"},
		DataTier:      "metrics",
		JitterSeconds: 30,
	}
	if err := Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Tenant != "saltest" || loaded.Email != "alice@acme.com" {
		t.Fatalf("round-trip mismatch: %+v", loaded)
	}
	if loaded.MachineID != cfg.MachineID {
		t.Fatalf("machine_id mismatch")
	}
}

func TestRejectsLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permission semantics not available on Windows")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.toml")
	cfg := &Config{ServerURL: "https://app.unitsense.ai", DeviceToken: "ust_dev_test"}
	_ = Save(cfgPath, cfg)
	if err := os.Chmod(cfgPath, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatalf("expected error on 0644 config; got nil")
	}
}

func TestSaveCreatesParentDirsWith0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permission check not applicable on Windows")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nested", "agent.toml")
	cfg := &Config{ServerURL: "https://app.unitsense.ai", DeviceToken: "ust_dev_test"}
	if err := Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(cfgPath)
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("parent dir should be 0700, got %o", info.Mode().Perm())
	}
}

func TestRejectsNonHTTPSURL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.toml")
	cfg := &Config{ServerURL: "http://example.com", DeviceToken: "ust_dev_x"}
	_ = Save(cfgPath, cfg)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatalf("expected error on non-HTTPS server.url")
	}
}

func TestAllowsLocalhostHTTP(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.toml")
	cfg := &Config{ServerURL: "http://localhost:3000", DeviceToken: "ust_dev_x"}
	_ = Save(cfgPath, cfg)
	_, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("localhost http should be allowed in dev: %v", err)
	}
}
