package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnitSense/agent/internal/parsers/claude_code"
	"github.com/spf13/cobra"
)

// statuslineInput is the subset of Claude Code's statusLine stdin JSON we care
// about. Claude Code (v2.1.132+) includes rate_limits for Pro/Max subscribers
// after the first API response of a session; each window may be absent.
// Schema: https://code.claude.com/docs/en/statusline.md
type statuslineInput struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	RateLimits *struct {
		FiveHour *statuslineWindow `json:"five_hour"`
		SevenDay *statuslineWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

type statuslineWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"` // unix epoch seconds
}

var statuslineCmd = &cobra.Command{
	Use:   "statusline",
	Short: "Claude Code statusLine command: render a line + capture live quota",
	Long: `Reads Claude Code's statusLine JSON on stdin, captures the live
rate-limit utilization (5-hour + weekly) so 'unitsense-agent run' can report
authoritative subscription quota, and prints a status line to stdout.

Wire it up in ~/.claude/settings.json:

  { "statusLine": { "type": "command", "command": "unitsense-agent statusline" } }`,
	// Never fail: a non-zero exit or noisy error would break the user's status
	// line. Capture is best-effort; rendering always succeeds.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, _ := io.ReadAll(os.Stdin)
		var in statuslineInput
		if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
			fmt.Fprint(os.Stdout, "unitsense")
			return nil
		}
		captureQuota(in)
		fmt.Fprint(os.Stdout, renderStatusline(in))
		return nil
	},
}

func init() { RegisterCommand(statuslineCmd) }

// ensureClaudeStatusline wires this binary as Claude Code's statusLine command
// so live subscription quota gets captured. Non-destructive: it never clobbers
// an existing statusLine (just tells the user how to add capture). Returns a
// human-readable note for setup to print, or "" if nothing to say.
func ensureClaudeStatusline() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".claude")
	path := filepath.Join(dir, "settings.json")

	self, err := os.Executable()
	if err != nil || self == "" {
		self = "unitsense-agent"
	}
	cmdStr := self + " statusline"

	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &settings) != nil {
			return fmt.Sprintf("Couldn't parse %s — to capture quota, add a statusLine: %q", path, cmdStr)
		}
	}
	if existing, ok := settings["statusLine"]; ok {
		if m, ok := existing.(map[string]any); ok {
			if c, _ := m["command"].(string); c == cmdStr || strings.Contains(c, "unitsense-agent statusline") {
				return "Claude Code statusLine already wired for quota capture."
			}
		}
		return fmt.Sprintf("You already have a Claude Code statusLine — to capture subscription quota, "+
			"call %q from it (it prints a status line on stdout).", cmdStr)
	}

	settings["statusLine"] = map[string]any{"type": "command", "command": cmdStr}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return ""
	}
	if os.WriteFile(path, append(data, '\n'), 0o644) != nil {
		return fmt.Sprintf("Couldn't write %s — to capture quota, add a statusLine: %q", path, cmdStr)
	}
	return "Wired Claude Code statusLine → live subscription quota (5h + weekly) will be captured."
}

// captureQuota persists the live rate-limit windows to the file the Claude
// parser reads. Best-effort and atomic; skips writing when no windows are
// present so it never clobbers a good capture with an empty one.
func captureQuota(in statuslineInput) {
	if in.RateLimits == nil || (in.RateLimits.FiveHour == nil && in.RateLimits.SevenDay == nil) {
		return
	}
	q := claude_code.CapturedQuota{CapturedAt: time.Now().UTC()}
	if w := in.RateLimits.FiveHour; w != nil {
		q.FiveHour = &claude_code.CapturedWindow{UsedPercent: w.UsedPercentage, ResetsAt: epoch(w.ResetsAt)}
	}
	if w := in.RateLimits.SevenDay; w != nil {
		q.SevenDay = &claude_code.CapturedWindow{UsedPercent: w.UsedPercentage, ResetsAt: epoch(w.ResetsAt)}
	}
	path := claude_code.QuotaFilePath()
	if path == "" {
		return
	}
	data, err := json.Marshal(q)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, path) // atomic replace
}

func epoch(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// renderStatusline builds the visible status line: working dir, model, and the
// two quota windows when present.
func renderStatusline(in statuslineInput) string {
	out := "unitsense"
	if d := in.Workspace.CurrentDir; d != "" {
		out = filepath.Base(d)
	}
	if m := in.Model.DisplayName; m != "" {
		out += " · " + m
	}
	if in.RateLimits != nil {
		if w := in.RateLimits.FiveHour; w != nil {
			out += fmt.Sprintf(" · 5h %.0f%%", w.UsedPercentage)
		}
		if w := in.RateLimits.SevenDay; w != nil {
			out += fmt.Sprintf(" · 7d %.0f%%", w.UsedPercentage)
		}
	}
	return out
}
