//go:build windows

package schedule

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const taskName = "UnitSense Agent"

// Install registers (or re-registers) the scheduled task. The task launches
// binPath through a small VBScript wrapper rather than a hidden PowerShell
// window. A hidden/non-interactive PowerShell invocation
// (`powershell.exe -WindowStyle Hidden -NonInteractive -Command "& '<path>' run"`)
// was found to be unreliable specifically when dispatched by Task Scheduler:
// Task Scheduler would report the run as successful, but the process would
// not reliably complete its work (confirmed by comparing against the same
// binary launched without PowerShell, which completed correctly every time).
// WScript.Shell.Run with window style 0 is the standard, more reliable way
// to launch a process fully hidden under Task Scheduler on Windows.
func Install(binPath string, interval time.Duration) error {
	minutes := int(interval.Minutes())
	if minutes < 1 {
		minutes = 1
	}

	launcherPath, err := writeLauncher(binPath)
	if err != nil {
		return fmt.Errorf("write launcher: %w", err)
	}

	tr := fmt.Sprintf(`wscript.exe //B "%s"`, launcherPath)
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

	if err := allowRunOnBattery(ctx); err != nil {
		return fmt.Errorf("configure battery settings: %w", err)
	}
	return nil
}

// allowRunOnBattery clears the task's "don't run on battery" conditions.
// `schtasks /Create` has no flag for this -- every call (including the
// agent's own automatic reconciliation, which re-runs schtasks /Create
// every time the interval changes) resets these to Windows' restrictive
// default, silently stopping the task from firing on an unplugged laptop.
// This is a one-shot PowerShell call made directly by the running process,
// not something Task Scheduler itself launches, so it isn't subject to the
// hidden/non-interactive dispatch unreliability documented above.
func allowRunOnBattery(ctx context.Context) error {
	script := fmt.Sprintf(
		`$t = Get-ScheduledTask -TaskName '%s'; $s = $t.Settings; `+
			`$s.DisallowStartIfOnBatteries = $false; $s.StopIfGoingOnBatteries = $false; `+
			`Set-ScheduledTask -TaskName '%s' -Settings $s | Out-Null`,
		taskName, taskName,
	)
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

// writeLauncher writes a VBScript next to binPath that runs it with "run",
// fully hidden (window style 0), waiting for it to finish so Task
// Scheduler's own run-tracking reflects the real process lifetime. It is
// regenerated on every Install call so it always points at the current
// binary path.
func writeLauncher(binPath string) (string, error) {
	launcherPath := filepath.Join(filepath.Dir(binPath), "unitsense-agent-run.vbs")
	escapedPath := strings.ReplaceAll(binPath, `"`, `""`)
	script := "Set s = CreateObject(\"WScript.Shell\")\r\n" +
		"s.Run \"\"\"" + escapedPath + "\"\" run\", 0, True\r\n"
	if err := os.WriteFile(launcherPath, []byte(script), 0600); err != nil {
		return "", err
	}
	return launcherPath, nil
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
