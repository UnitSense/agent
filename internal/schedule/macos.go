//go:build darwin

package schedule

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const plistTpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>ai.unitsense.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>run</string>
  </array>
  <key>StartInterval</key><integer>%d</integer>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
  <key>RunAtLoad</key><false/>
</dict></plist>
`

func Install(binPath string, interval time.Duration) error {
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "ai.unitsense.agent.plist")
	logPath := filepath.Join(home, "Library", "Logs", "unitsense-agent.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	_ = os.MkdirAll(filepath.Dir(plistPath), 0755)
	content := fmt.Sprintf(plistTpl, binPath, int(interval.Seconds()), logPath, logPath)
	if err := os.WriteFile(plistPath, []byte(content), 0644); err != nil {
		return err
	}
	uid := os.Getuid()
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d", uid), plistPath).Run()
	if err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	return nil
}

func Uninstall() error {
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "ai.unitsense.agent.plist")
	uid := os.Getuid()
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d", uid), plistPath).Run()
	return os.Remove(plistPath)
}
