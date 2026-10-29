package cli

import (
	"bytes"
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

'unitsense-agent setup' wires this up in ~/.claude/settings.json for you:

  { "statusLine": { "type": "command", "command": "unitsense-agent statusline" } }

If you already have your own statusLine, setup keeps it: it saves your command
and this one runs it for you after capturing quota, printing its output so your
status line looks the same. 'unitsense-agent reset' puts your original back.`,
	// Never fail: a non-zero exit or noisy error would break the user's status
	// line. Capture is best-effort; rendering always succeeds.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, _ := io.ReadAll(os.Stdin)
		in, parsed := parseStatuslineInput(raw)
		if parsed {
			captureQuota(in)
		}
		// The user's own statusLine (if setup chained one) decides what is
		// shown; ours is only the fallback.
		if out, ok := runChainedStatusline(raw); ok {
			_, _ = os.Stdout.Write(out)
			return nil
		}
		if !parsed {
			fmt.Fprint(os.Stdout, "unitsense")
			return nil
		}
		fmt.Fprint(os.Stdout, renderStatusline(in))
		return nil
	},
}

func init() { RegisterCommand(statuslineCmd) }

// parseStatuslineInput decodes Claude Code's statusLine JSON. Some Windows
// shells prefix piped text with a UTF-8 byte-order mark, which encoding/json
// rejects, so it is stripped first and a valid payload still parses.
func parseStatuslineInput(raw []byte) (statuslineInput, bool) {
	var in statuslineInput
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return in, false
	}
	return in, true
}

// isUnitsenseStatuslineCommand reports whether c looks like a prior
// "<some unitsense-agent binary> statusline" entry, regardless of exact
// path or a ".exe" suffix on Windows. A naive literal substring check for
// "unitsense-agent statusline" never matches a real Windows path (it's
// always "unitsense-agent.exe statusline"), so this checks the two parts
// independently instead.
func isUnitsenseStatuslineCommand(c string) bool {
	return strings.Contains(c, "unitsense-agent") && strings.HasSuffix(strings.TrimSpace(c), "statusline")
}

// selfStatuslineCommand is the exact statusLine command that runs THIS binary.
// The executable path is quoted (never the "statusline" argument) so it works
// when the install path or username contains spaces: Claude Code runs a
// "type": "command" statusLine through a shell, where an unquoted path would
// be split at the space and fail silently. fmt's %q is NOT used -- it
// Go-escapes backslashes, which would corrupt a literal Windows path.
func selfStatuslineCommand() string {
	self, err := os.Executable()
	if err != nil || self == "" {
		self = "unitsense-agent"
	}
	return `"` + self + `" statusline`
}

// ensureClaudeStatusline wires this binary as Claude Code's statusLine command
// so live subscription quota gets captured. Non-destructive: an existing
// statusLine of the user's is never lost -- it is saved and run by this
// binary after capture (see statusline_chain.go), so their status line keeps
// working and `reset` can restore it. A dead entry of ours (e.g. a leftover
// test binary) is repaired in place. Returns a human-readable note for setup
// to print, or "" if nothing to say.
func ensureClaudeStatusline() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".claude")
	path := filepath.Join(dir, "settings.json")

	cmdStr := selfStatuslineCommand()

	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &settings) != nil {
			return fmt.Sprintf("Couldn't parse %s — to capture quota, add a statusLine: %q", path, cmdStr)
		}
	}
	if existing, ok := settings["statusLine"]; ok {
		if m, ok := existing.(map[string]any); ok {
			c, _ := m["command"].(string)
			switch {
			case c == cmdStr:
				if loadChain() != nil {
					return "Claude Code statusLine already wired for quota capture (your own status line is kept and still runs)."
				}
				return "Claude Code statusLine already wired for quota capture."
			case isUnitsenseStatuslineCommand(c) || isDeadStatuslineCommand(c):
				// Points at unitsense-agent but from a stale binary path (e.g.
				// a reinstall moved the binary to a new location), or at an
				// executable that no longer exists at all (e.g. a leftover test
				// binary) -- rewrite it in place rather than silently leaving
				// quota capture broken, which is exactly what happened before.
				m["command"] = cmdStr
				settings["statusLine"] = m
				data, err := json.MarshalIndent(settings, "", "  ")
				if err == nil && os.WriteFile(path, append(data, '\n'), 0o644) == nil {
					return "Updated Claude Code statusLine — it was pointing at a stale binary location."
				}
				return fmt.Sprintf("statusLine points at a stale path (%s) — couldn't rewrite it, re-add manually: %q", c, cmdStr)
			case c != "":
				// The user's own status line. Keep it: save it verbatim, point
				// Claude Code at us, and run theirs after capturing quota.
				if err := saveChain(m); err != nil {
					return fmt.Sprintf("You already have a Claude Code statusLine, and saving it failed (%v) — "+
						"to capture subscription quota, call %q from it (it prints a status line on stdout).", err, cmdStr)
				}
				wrapped := map[string]any{}
				for k, v := range m {
					wrapped[k] = v
				}
				wrapped["command"] = cmdStr
				settings["statusLine"] = wrapped
				data, err := json.MarshalIndent(settings, "", "  ")
				if err != nil || os.WriteFile(path, append(data, '\n'), 0o644) != nil {
					removeChain() // nothing changed in settings, so don't leave a stale copy
					return fmt.Sprintf("You already have a Claude Code statusLine — couldn't update %s; "+
						"to capture subscription quota, call %q from it.", path, cmdStr)
				}
				return "Kept your existing Claude Code statusLine and added live quota capture to it " +
					"(your status line looks the same). `unitsense-agent reset` restores the original."
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
