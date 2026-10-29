//go:build !windows

package cli

import (
	"context"
	"os/exec"
)

// shellCommand runs a user's statusLine command the way Claude Code does:
// through a shell, so scripts, pipes and arguments behave as the user wrote them.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}

// resolvedShell names the shell shellCommand uses; tests use it to pick
// commands that are valid in that shell.
func resolvedShell() string { return "sh" }
