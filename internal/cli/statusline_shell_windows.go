//go:build windows

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// On Windows, Claude Code runs statusLine commands through Git Bash when Git
// Bash is installed, and through PowerShell otherwise
// (https://code.claude.com/docs/en/statusline#windows-configuration). A user's
// own statusLine command is written for that shell -- a bash script path, a
// `powershell -File ...` line -- so chain it through the SAME shell, never
// cmd.exe, or their command may silently produce nothing.

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// gitBashLookup is a variable so tests can force the PowerShell fallback on a
// machine that has Git Bash installed.
var gitBashLookup = findGitBash

func gitBashPath() string { return gitBashLookup() }

// findGitBash finds Git for Windows' bash.exe the way a user would expect:
// an explicit override first, then the standard install locations, then
// relative to git.exe on PATH. It deliberately does NOT use whatever `bash`
// is first on PATH -- on many machines that is the WSL launcher in System32,
// which runs a Linux distro and is not the shell Claude Code uses.
func findGitBash() string {
	if p := os.Getenv("CLAUDE_CODE_GIT_BASH_PATH"); p != "" && fileExists(p) {
		return p
	}
	for _, base := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs"),
	} {
		if base == "" || base == "Programs" {
			continue
		}
		if p := filepath.Join(base, "Git", "bin", "bash.exe"); fileExists(p) {
			return p
		}
	}
	if git, err := exec.LookPath("git"); err == nil {
		// <root>\cmd\git.exe or <root>\bin\git.exe -> <root>\bin\bash.exe
		root := filepath.Dir(filepath.Dir(git))
		if p := filepath.Join(root, "bin", "bash.exe"); fileExists(p) {
			return p
		}
	}
	return ""
}

// resolvedShell names the shell shellCommand will use ("bash" or "powershell").
func resolvedShell() string {
	if gitBashPath() != "" {
		return "bash"
	}
	return "powershell"
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	var cmd *exec.Cmd
	if bash := gitBashPath(); bash != "" {
		cmd = exec.CommandContext(ctx, bash, "-c", command)
	} else {
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
