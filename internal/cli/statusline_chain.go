package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnitSense/agent/internal/config"
)

// Chaining lets quota capture coexist with a statusLine the user already had.
//
// Claude Code only runs ONE statusLine command. When the user already has
// their own, setup used to leave it alone and quota was never captured. Now
// setup stores the user's statusLine object here, untouched, and points Claude
// Code at `unitsense-agent statusline`; at runtime that command captures quota
// from Claude Code's JSON, then runs the saved command with the same stdin and
// prints its output, so the user's status line looks exactly as before.
//
// settings.json always holds the same `"<agent>" statusline` command, chained
// or not -- whether to chain is decided by whether this file exists, which
// keeps detection, idempotency and stale-path repair simple and avoids
// nesting one shell's quoting inside another's.

// chainTimeout bounds how long a user's own statusLine command may run so a
// hung script can never freeze Claude Code's status area.
const chainTimeout = 3 * time.Second

// chainRecord is the user's original statusLine object, stored verbatim so
// `reset` can restore every key (command, type, padding, ...).
type chainRecord struct {
	Original map[string]any `json:"original"`
	SavedAt  time.Time      `json:"saved_at"`
}

func (r *chainRecord) command() string {
	c, _ := r.Original["command"].(string)
	return c
}

// chainFilePath lives next to agent.toml so `reset` (which removes that whole
// directory) and the agent's own config always agree on where it is.
func chainFilePath() string {
	p, err := config.DefaultPath()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "statusline-chain.json")
}

func loadChain() *chainRecord {
	path := chainFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rec chainRecord
	if json.Unmarshal(data, &rec) != nil || rec.Original == nil {
		return nil
	}
	return &rec
}

func saveChain(original map[string]any) error {
	path := chainFilePath()
	if path == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(chainRecord{Original: original, SavedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removeChain() {
	if path := chainFilePath(); path != "" {
		_ = os.Remove(path)
	}
}

// runChainedStatusline runs the user's saved statusLine command with the same
// stdin Claude Code gave us and returns its stdout. ok is false when there is
// nothing to chain, or the command produced no output, so the caller can fall
// back to our own rendering and never leave the status area blank.
func runChainedStatusline(raw []byte) (out []byte, ok bool) {
	rec := loadChain()
	if rec == nil {
		return nil, false
	}
	command := strings.TrimSpace(rec.command())
	// Never chain to ourselves -- that would recurse forever.
	if command == "" || isUnitsenseStatuslineCommand(command) {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), chainTimeout)
	defer cancel()
	cmd := shellCommand(ctx, command)
	cmd.Stdin = bytes.NewReader(raw)
	// If the timeout kills the shell but a child it started still holds the
	// output pipe open, Wait would otherwise block until that child exits.
	// WaitDelay bounds it so a hung status line can't hang us either.
	cmd.WaitDelay = 500 * time.Millisecond
	// A non-zero exit that still printed something is the user's business;
	// only an empty result falls back to our own line.
	out, _ = cmd.Output()
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, false
	}
	return out, true
}

// isDeadStatuslineCommand reports an entry shaped like `"<exe>" statusline`
// whose executable path is absolute and no longer exists -- e.g. the Go test
// binary an older test run left behind (...\go-build...\cli.test.exe). Such an
// entry can never work, so it is repaired rather than treated as the user's
// own status line (which would chain a command that can only fail).
func isDeadStatuslineCommand(c string) bool {
	c = strings.TrimSpace(c)
	if !strings.HasSuffix(c, " statusline") {
		return false
	}
	exe := strings.Trim(strings.TrimSpace(strings.TrimSuffix(c, " statusline")), `"`)
	if exe == "" || !filepath.IsAbs(exe) {
		return false
	}
	_, err := os.Stat(exe)
	return os.IsNotExist(err)
}

// restoreClaudeStatusline undoes chaining: it puts the user's original
// statusLine back into Claude Code's settings and removes the saved copy.
// Called by `reset` before it deletes the config directory. Returns a note
// for the user, or "" if nothing was chained.
func restoreClaudeStatusline() string {
	rec := loadChain()
	if rec == nil {
		return ""
	}
	defer removeChain()

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	settings := map[string]any{}
	if json.Unmarshal(data, &settings) != nil {
		return "Couldn't parse " + path + " — your original statusLine is saved in " + chainFilePath()
	}
	current, _ := settings["statusLine"].(map[string]any)
	cmd, _ := current["command"].(string)
	// Only put it back if the entry is still ours; if the user has since
	// pointed statusLine somewhere else, leave their newer choice alone.
	if cmd != selfStatuslineCommand() && !isUnitsenseStatuslineCommand(cmd) && !isDeadStatuslineCommand(cmd) {
		return ""
	}
	settings["statusLine"] = rec.Original
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil || os.WriteFile(path, append(out, '\n'), 0o644) != nil {
		return "Couldn't restore your Claude Code statusLine — it is saved in " + chainFilePath()
	}
	return "Restored your original Claude Code statusLine."
}
