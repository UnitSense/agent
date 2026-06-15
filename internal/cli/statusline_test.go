package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readStatusLineCmd(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return ""
	}
	var s struct {
		StatusLine struct {
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return s.StatusLine.Command
}

func TestEnsureClaudeStatuslineSetsWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	note := ensureClaudeStatusline()
	if !strings.Contains(note, "Wired") {
		t.Errorf("note = %q, want a 'Wired' confirmation", note)
	}
	if cmd := readStatusLineCmd(t, home); !strings.HasSuffix(cmd, "statusline") {
		t.Errorf("statusLine command = %q, want it to end in 'statusline'", cmd)
	}
}

func TestEnsureClaudeStatuslineDoesNotClobber(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"statusLine":{"type":"command","command":"my-custom-statusline.sh"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	note := ensureClaudeStatusline()
	if !strings.Contains(note, "already have") {
		t.Errorf("note = %q, want an 'already have' notice", note)
	}
	if cmd := readStatusLineCmd(t, home); cmd != "my-custom-statusline.sh" {
		t.Errorf("existing statusLine was clobbered: command = %q", cmd)
	}
}

func TestEnsureClaudeStatuslineIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_ = ensureClaudeStatusline()
	note := ensureClaudeStatusline()
	if !strings.Contains(note, "already wired") {
		t.Errorf("second call note = %q, want 'already wired'", note)
	}
}
