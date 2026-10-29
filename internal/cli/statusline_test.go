package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setTestHome points the user's home at dir for the duration of a test. Go's
// os.UserHomeDir reads $HOME on Unix but %USERPROFILE% on Windows, so BOTH
// must be set: setting only HOME on Windows silently leaves the real home in
// place, and these tests would write into the developer's actual
// ~/.claude/settings.json (which is how a stale "...\cli.test.exe statusline"
// entry once ended up in a real Claude Code config).
func setTestHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if got, err := os.UserHomeDir(); err != nil || got != dir {
		t.Fatalf("test home not isolated: UserHomeDir() = %q, want %q (err %v)", got, dir, err)
	}
}

func writeClaudeSettings(t *testing.T, home, contents string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readStatusLine(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return nil
	}
	var s struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return s.StatusLine
}

func readStatusLineCmd(t *testing.T, home string) string {
	t.Helper()
	c, _ := readStatusLine(t, home)["command"].(string)
	return c
}

func TestEnsureClaudeStatuslineSetsWhenAbsent(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	note := ensureClaudeStatusline()
	if !strings.Contains(note, "Wired") {
		t.Errorf("note = %q, want a 'Wired' confirmation", note)
	}
	if cmd := readStatusLineCmd(t, home); !strings.HasSuffix(cmd, "statusline") {
		t.Errorf("statusLine command = %q, want it to end in 'statusline'", cmd)
	}
	if loadChain() != nil {
		t.Error("nothing should be chained when the user had no statusLine")
	}
}

func TestEnsureClaudeStatuslineChainsUsersOwn(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	writeClaudeSettings(t, home,
		`{"theme":"dark","statusLine":{"type":"command","command":"my-custom-statusline.sh","padding":2}}`)

	note := ensureClaudeStatusline()
	if !strings.Contains(note, "Kept your existing") {
		t.Errorf("note = %q, want a 'Kept your existing' notice", note)
	}

	// Claude Code now runs us...
	sl := readStatusLine(t, home)
	if cmd, _ := sl["command"].(string); !strings.HasSuffix(cmd, " statusline") || strings.Contains(cmd, "my-custom") {
		t.Errorf("statusLine command = %q, want ours", cmd)
	}
	// ...other keys of their statusLine, and the rest of their settings, survive.
	if sl["type"] != "command" || sl["padding"] != float64(2) {
		t.Errorf("statusLine keys not preserved: %v", sl)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(data), `"theme": "dark"`) {
		t.Errorf("unrelated settings lost: %s", data)
	}

	// ...and their original is saved verbatim for us to run (and for reset).
	rec := loadChain()
	if rec == nil || rec.command() != "my-custom-statusline.sh" || rec.Original["padding"] != float64(2) {
		t.Errorf("chain record = %+v, want the user's original statusLine", rec)
	}
}

func TestEnsureClaudeStatuslineChainIsIdempotent(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	writeClaudeSettings(t, home, `{"statusLine":{"type":"command","command":"my-custom-statusline.sh"}}`)

	_ = ensureClaudeStatusline()
	note := ensureClaudeStatusline()
	if !strings.Contains(note, "already wired") {
		t.Errorf("second call note = %q, want 'already wired'", note)
	}
	// The saved original must still be theirs, never overwritten with ours.
	if rec := loadChain(); rec == nil || rec.command() != "my-custom-statusline.sh" {
		t.Errorf("chain record after second call = %+v", rec)
	}
}

func TestEnsureClaudeStatuslineRewritesStalePath(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	// Simulates a prior install whose binary has since moved (e.g. reinstalled
	// via a different path) -- the statusLine entry is stale but still ours.
	writeClaudeSettings(t, home,
		`{"statusLine":{"type":"command","command":"C:\\old\\path\\unitsense-agent.exe statusline"}}`)

	note := ensureClaudeStatusline()
	if !strings.Contains(note, "Updated") {
		t.Errorf("note = %q, want an 'Updated' confirmation", note)
	}
	if cmd := readStatusLineCmd(t, home); strings.Contains(cmd, "old\\path") {
		t.Errorf("stale statusLine command was not rewritten: %q", cmd)
	}
	if loadChain() != nil {
		t.Error("a stale entry of ours must be repaired, not chained")
	}
}

func TestEnsureClaudeStatuslineRepairsDeadEntry(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	// An absolute path to an executable that no longer exists, shaped like the
	// leftover Go test binary: not "ours" by name, but it can never work.
	dead := filepath.Join(t.TempDir(), "go-build123", "cli.test")
	settings, _ := json.Marshal(map[string]any{
		"statusLine": map[string]any{"type": "command", "command": `"` + dead + `" statusline`},
	})
	writeClaudeSettings(t, home, string(settings))

	note := ensureClaudeStatusline()
	if !strings.Contains(note, "Updated") {
		t.Errorf("note = %q, want an 'Updated' confirmation", note)
	}
	if cmd := readStatusLineCmd(t, home); strings.Contains(cmd, "go-build123") {
		t.Errorf("dead statusLine command was not rewritten: %q", cmd)
	}
	if loadChain() != nil {
		t.Error("a dead entry must be repaired, not chained")
	}
}

func TestEnsureClaudeStatuslineIdempotent(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	_ = ensureClaudeStatusline()
	note := ensureClaudeStatusline()
	if !strings.Contains(note, "already wired") {
		t.Errorf("second call note = %q, want 'already wired'", note)
	}
}

func TestIsDeadStatuslineCommand(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "gone", "agent")
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"missing absolute path, quoted", `"` + gone + `" statusline`, true},
		{"missing absolute path, unquoted", gone + ` statusline`, true},
		{"existing executable", `"` + self + `" statusline`, false},
		{"relative or bare name", `unitsense-agent statusline`, false},
		{"user's own script", `/usr/local/bin/my-statusline`, false},
		{"missing path but not a statusline call", `"` + gone + `" --other`, false},
	}
	for _, c := range cases {
		if got := isDeadStatuslineCommand(c.cmd); got != c.want {
			t.Errorf("%s: isDeadStatuslineCommand(%q) = %v, want %v", c.name, c.cmd, got, c.want)
		}
	}
}

func TestRunChainedStatuslineRunsSavedCommand(t *testing.T) {
	setTestHome(t, t.TempDir())
	if err := saveChain(map[string]any{"type": "command", "command": "echo chained-ok"}); err != nil {
		t.Fatal(err)
	}
	out, ok := runChainedStatusline([]byte(`{}`))
	if !ok || !strings.Contains(string(out), "chained-ok") {
		t.Errorf("runChainedStatusline = %q, %v; want output containing 'chained-ok'", out, ok)
	}
}

func TestRunChainedStatuslinePassesStdinThrough(t *testing.T) {
	setTestHome(t, t.TempDir())
	// A command that prints whatever it reads on stdin, in the shell the
	// agent will actually use (sh / Git Bash, or PowerShell on Windows).
	echoStdin := "cat"
	if resolvedShell() == "powershell" {
		echoStdin = `[Console]::In.ReadToEnd()`
	}
	if err := saveChain(map[string]any{"type": "command", "command": echoStdin}); err != nil {
		t.Fatal(err)
	}
	out, ok := runChainedStatusline([]byte(`{"model":"claude-test"}`))
	if !ok || !strings.Contains(string(out), "claude-test") {
		t.Errorf("stdin was not passed to the user's command: out=%q ok=%v", out, ok)
	}
}

func TestRunChainedStatuslineFallsBackWhenNoOutput(t *testing.T) {
	setTestHome(t, t.TempDir())
	silent := "true"
	if resolvedShell() == "powershell" {
		silent = "$null"
	}
	if err := saveChain(map[string]any{"type": "command", "command": silent}); err != nil {
		t.Fatal(err)
	}
	if out, ok := runChainedStatusline([]byte(`{}`)); ok {
		t.Errorf("a command with no output must fall back to our own line, got %q", out)
	}
}

func TestParseStatuslineInput(t *testing.T) {
	payload := `{"model":{"display_name":"Opus"},"rate_limits":{"five_hour":{"used_percentage":42,"resets_at":1893456000}}}`
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"plain JSON", payload, true},
		{"UTF-8 byte-order mark (PowerShell pipe)", "\xEF\xBB\xBF" + payload + "\r\n", true},
		{"empty", "", false},
		{"not JSON", "hello", false},
	}
	for _, c := range cases {
		in, ok := parseStatuslineInput([]byte(c.raw))
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
		}
		if c.ok && (in.Model.DisplayName != "Opus" || in.RateLimits == nil || in.RateLimits.FiveHour == nil ||
			in.RateLimits.FiveHour.UsedPercentage != 42) {
			t.Errorf("%s: payload not decoded: %+v", c.name, in)
		}
	}
}

func TestRunChainedStatuslineHungCommandTimesOut(t *testing.T) {
	setTestHome(t, t.TempDir())
	hang := "sleep 10"
	if resolvedShell() == "powershell" {
		hang = "Start-Sleep -Seconds 10"
	}
	if err := saveChain(map[string]any{"type": "command", "command": hang}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, ok := runChainedStatusline([]byte(`{}`))
	elapsed := time.Since(start)
	if ok {
		t.Error("a hung command must fall back to our own line")
	}
	// Bounded by chainTimeout (+ the pipe grace period), not by the 10s sleep.
	if elapsed > chainTimeout+3*time.Second {
		t.Errorf("hung command held us for %v, want about %v", elapsed, chainTimeout)
	}
}

func TestRunChainedStatuslineNothingSaved(t *testing.T) {
	setTestHome(t, t.TempDir())
	if _, ok := runChainedStatusline([]byte(`{}`)); ok {
		t.Error("with no chain saved there is nothing to run")
	}
}

func TestRunChainedStatuslineNeverChainsToItself(t *testing.T) {
	setTestHome(t, t.TempDir())
	// Would recurse forever if followed.
	if err := saveChain(map[string]any{"command": `"/x/unitsense-agent" statusline`}); err != nil {
		t.Fatal(err)
	}
	if _, ok := runChainedStatusline([]byte(`{}`)); ok {
		t.Error("a chain pointing back at unitsense-agent statusline must be ignored")
	}
}

func TestRestoreClaudeStatuslinePutsOriginalBack(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	writeClaudeSettings(t, home,
		`{"statusLine":{"type":"command","command":"my-custom-statusline.sh","padding":2}}`)
	_ = ensureClaudeStatusline()

	note := restoreClaudeStatusline()
	if !strings.Contains(note, "Restored") {
		t.Errorf("note = %q, want a 'Restored' confirmation", note)
	}
	sl := readStatusLine(t, home)
	if sl["command"] != "my-custom-statusline.sh" || sl["padding"] != float64(2) {
		t.Errorf("original statusLine not restored: %v", sl)
	}
	if loadChain() != nil {
		t.Error("saved copy should be removed after restore")
	}
}

func TestRestoreClaudeStatuslineLeavesNewerChoiceAlone(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	writeClaudeSettings(t, home, `{"statusLine":{"type":"command","command":"my-custom-statusline.sh"}}`)
	_ = ensureClaudeStatusline()
	// The user later points statusLine at something else entirely.
	writeClaudeSettings(t, home, `{"statusLine":{"type":"command","command":"something-newer.sh"}}`)

	if note := restoreClaudeStatusline(); note != "" {
		t.Errorf("note = %q, want nothing to say", note)
	}
	if cmd := readStatusLineCmd(t, home); cmd != "something-newer.sh" {
		t.Errorf("the user's newer statusLine was overwritten: %q", cmd)
	}
}

func TestRestoreClaudeStatuslineNothingChained(t *testing.T) {
	setTestHome(t, t.TempDir())
	if note := restoreClaudeStatusline(); note != "" {
		t.Errorf("note = %q, want nothing when nothing was chained", note)
	}
}
