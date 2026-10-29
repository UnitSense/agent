//go:build windows

package cli

import (
	"strings"
	"testing"
)

// Claude Code runs statusLine commands through Git Bash when it is installed
// and PowerShell otherwise; chaining must use the same shell. These tests
// exercise both routes on a machine that may have either.

func TestChainedStatuslineViaPowerShellFallback(t *testing.T) {
	setTestHome(t, t.TempDir())
	old := gitBashLookup
	gitBashLookup = func() string { return "" } // pretend Git Bash is absent
	t.Cleanup(func() { gitBashLookup = old })
	if got := resolvedShell(); got != "powershell" {
		t.Fatalf("resolvedShell() = %q, want powershell", got)
	}

	// stdin is passed through to the user's command.
	if err := saveChain(map[string]any{"type": "command", "command": `[Console]::In.ReadToEnd()`}); err != nil {
		t.Fatal(err)
	}
	out, ok := runChainedStatusline([]byte(`{"model":"claude-ps"}`))
	if !ok || !strings.Contains(string(out), "claude-ps") {
		t.Errorf("PowerShell stdin passthrough: out=%q ok=%v", out, ok)
	}

	// quotes survive the trip through the argument list.
	if err := saveChain(map[string]any{"type": "command", "command": `Write-Output 'it''s two  spaces'`}); err != nil {
		t.Fatal(err)
	}
	out, ok = runChainedStatusline([]byte(`{}`))
	if !ok || !strings.Contains(string(out), "it's two  spaces") {
		t.Errorf("PowerShell quoting: out=%q ok=%v", out, ok)
	}
}

func TestChainedStatuslineViaGitBash(t *testing.T) {
	if findGitBash() == "" {
		t.Skip("Git Bash is not installed on this machine")
	}
	setTestHome(t, t.TempDir())
	if got := resolvedShell(); got != "bash" {
		t.Fatalf("resolvedShell() = %q, want bash", got)
	}

	if err := saveChain(map[string]any{"type": "command", "command": `cat`}); err != nil {
		t.Fatal(err)
	}
	out, ok := runChainedStatusline([]byte(`{"model":"claude-bash"}`))
	if !ok || !strings.Contains(string(out), "claude-bash") {
		t.Errorf("Git Bash stdin passthrough: out=%q ok=%v", out, ok)
	}

	if err := saveChain(map[string]any{"type": "command", "command": `echo "two  spaces" 'single quoted'`}); err != nil {
		t.Fatal(err)
	}
	out, ok = runChainedStatusline([]byte(`{}`))
	if !ok || !strings.Contains(string(out), "two  spaces single quoted") {
		t.Errorf("Git Bash quoting: out=%q ok=%v", out, ok)
	}
}
