package hermes

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDBPath resolves Hermes Agent's state.db under its default home
// directory. Unlike Claude Code/Codex (always "~/.claude", "~/.codex" on
// every OS), Hermes itself resolves differently on Windows — confirmed by
// reading hermes_constants.py's _get_platform_default_hermes_home()
// directly: on win32 it's "%LOCALAPPDATA%/hermes" (falling back to
// "<home>/AppData/Local/hermes" when LOCALAPPDATA is unset), and
// "~/.hermes" everywhere else. This does not account for a custom
// HERMES_HOME override or named profiles (<root>/profiles/<name>) — both
// real Hermes features we found by hand but haven't needed to support yet.
func DefaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	var root string
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		root = filepath.Join(localAppData, "hermes")
	} else {
		root = filepath.Join(home, ".hermes")
	}

	return filepath.Join(root, "state.db"), nil
}
