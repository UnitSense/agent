//go:build linux

package schedule

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const serviceTpl = `[Unit]
Description=UnitSense Agent — one-shot sync
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s run
Nice=10
StandardOutput=append:%s/.config/unitsense/agent.log
StandardError=append:%s/.config/unitsense/agent.log
`

const timerTpl = `[Unit]
Description=Run UnitSense Agent every %d seconds

[Timer]
OnBootSec=2min
OnUnitActiveSec=%ds
RandomizedDelaySec=30s
Persistent=true

[Install]
WantedBy=timers.target
`

func Install(binPath string, interval time.Duration) error {
	home, _ := os.UserHomeDir()
	systemdDir := filepath.Join(home, ".config", "systemd", "user")
	_ = os.MkdirAll(systemdDir, 0755)

	service := fmt.Sprintf(serviceTpl, binPath, home, home)
	if err := os.WriteFile(filepath.Join(systemdDir, "unitsense-agent.service"), []byte(service), 0644); err != nil {
		return err
	}
	timer := fmt.Sprintf(timerTpl, int(interval.Seconds()), int(interval.Seconds()))
	if err := os.WriteFile(filepath.Join(systemdDir, "unitsense-agent.timer"), []byte(timer), 0644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	if err := exec.Command("systemctl", "--user", "enable", "--now", "unitsense-agent.timer").Run(); err != nil {
		return fmt.Errorf("systemctl enable: %w", err)
	}
	return nil
}

func Uninstall() error {
	_ = exec.Command("systemctl", "--user", "disable", "--now", "unitsense-agent.timer").Run()
	home, _ := os.UserHomeDir()
	systemdDir := filepath.Join(home, ".config", "systemd", "user")
	_ = os.Remove(filepath.Join(systemdDir, "unitsense-agent.timer"))
	_ = os.Remove(filepath.Join(systemdDir, "unitsense-agent.service"))
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}
