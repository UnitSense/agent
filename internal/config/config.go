package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
)

type Config struct {
	ServerURL     string    `toml:"server_url"`
	DeviceToken   string    `toml:"device_token"`
	Tenant        string    `toml:"tenant"`
	Email         string    `toml:"email"`
	MachineID     uuid.UUID `toml:"machine_id"`
	MachineLabel  string    `toml:"machine_label,omitempty"`
	Providers     []string  `toml:"providers"`
	DataTier      string    `toml:"data_tier"`
	JitterSeconds int       `toml:"jitter_seconds"`
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "unitsense", "agent.toml"), nil
}

func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("config file %s has loose permissions %o; expected 0600", path, info.Mode().Perm())
	}
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, err
	}
	if err := validateURL(c.ServerURL); err != nil {
		return nil, err
	}
	if c.DataTier == "" {
		c.DataTier = "metrics"
	}
	if c.DataTier != "metrics" {
		return nil, fmt.Errorf("unsupported data_tier %q (v0 only supports 'metrics')", c.DataTier)
	}
	return &c, nil
}

func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid server_url: %w", err)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && (u.Hostname() == "localhost" || strings.HasPrefix(u.Hostname(), "127.") || u.Hostname() == "::1") {
		return nil
	}
	return fmt.Errorf("server_url must be HTTPS (got %s)", u.Scheme)
}
