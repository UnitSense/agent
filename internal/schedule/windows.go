//go:build windows

package schedule

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const taskName = "UnitSense Agent"

func Install(binPath string, interval time.Duration) error {
	minutes := int(interval.Minutes())
	if minutes < 1 {
		minutes = 1
	}
	// Wrap in a hidden PowerShell window so Task Scheduler doesn't flash a
	// terminal on every run. The path is single-quoted in the -Command string,
	// so escape embedded single-quotes by doubling them ('').
	escapedPath := strings.ReplaceAll(binPath, `'`, `''`)
	tr := fmt.Sprintf(`powershell.exe -WindowStyle Hidden -NonInteractive -Command "& '%s' run"`, escapedPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "schtasks",
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "schtasks", "/Delete", "/TN", taskName, "/F")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /Delete failed: %w\n%s", err, out)
	}
	return nil
}
