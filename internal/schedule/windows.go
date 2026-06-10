//go:build windows

package schedule

import (
	"fmt"
	"os/exec"
	"time"
)

const taskName = "UnitSense Agent"

func Install(binPath string, interval time.Duration) error {
	minutes := int(interval.Minutes())
	if minutes < 1 {
		minutes = 1
	}
	// Wrap in a hidden PowerShell window so Task Scheduler doesn't flash
	// a terminal every time the agent runs.
	tr := fmt.Sprintf(`powershell.exe -WindowStyle Hidden -NonInteractive -Command "& '%s' run"`, binPath)
	cmd := exec.Command("schtasks",
		"/Create",
		"/TN", taskName,
		"/TR", tr,
		"/SC", "MINUTE",
		"/MO", fmt.Sprintf("%d", minutes),
		"/F",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /Create failed: %w\n%s", err, out)
	}
	return nil
}

func Uninstall() error {
	cmd := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /Delete failed: %w\n%s", err, out)
	}
	return nil
}
