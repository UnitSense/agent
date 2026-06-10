//go:build windows

package schedule

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestInstallCreatesTask(t *testing.T) {
	bin := `C:\Windows\System32\cmd.exe`
	t.Cleanup(func() { _ = Uninstall() })
	if err := Install(bin, 10*time.Minute); err != nil {
		t.Fatalf("Install: %v", err)
	}
	out, err := exec.Command("schtasks", "/Query", "/TN", taskName, "/FO", "LIST").CombinedOutput()
	if err != nil {
		t.Fatalf("task not found after Install: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "cmd.exe") {
		t.Fatalf("task command not found in output: %s", out)
	}
}

func TestUninstallRemovesTask(t *testing.T) {
	bin := `C:\Windows\System32\cmd.exe`
	_ = Install(bin, 10*time.Minute)
	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	out, _ := exec.Command("schtasks", "/Query", "/TN", taskName, "/FO", "LIST").CombinedOutput()
	if strings.Contains(string(out), taskName) {
		t.Fatalf("task still present after Uninstall: %s", out)
	}
}
