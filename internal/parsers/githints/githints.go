// Package githints shells out to git to collect lightweight repo context for
// a session. All network-identifiable data (remote URL) is sha256-hashed before
// returning. Branch names and commit SHAs are returned as-is per the F2 spec
// privacy contract.
package githints

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Hints holds the git context extracted for a single session.
type Hints struct {
	// RepoRemoteHash is sha256(normalized remote URL). Empty when the
	// workspace is not a git repo or has no remote configured.
	RepoRemoteHash string
	// BranchName is the current branch (e.g. "main"). Empty on detached HEAD
	// or when git is unavailable.
	BranchName string
	// CommitSHAs are short commit hashes authored between since and until.
	// Empty when none exist or git fails.
	CommitSHAs []string
}

// Extract runs git commands against workspacePath and returns git hints for the
// given time window. All errors are suppressed; partial results are returned.
// If git is not installed or workspacePath is not a git repo, Hints{} is returned.
func Extract(workspacePath string, since, until time.Time) Hints {
	if workspacePath == "" {
		return Hints{}
	}

	var h Hints

	// Branch name
	if branch, err := gitCmd(workspacePath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		h.BranchName = strings.TrimSpace(branch)
		// Detached HEAD is not useful
		if h.BranchName == "HEAD" {
			h.BranchName = ""
		}
	}

	// Remote origin URL -> sha256
	if remote, err := gitCmd(workspacePath, "remote", "get-url", "origin"); err == nil {
		raw := strings.TrimSpace(remote)
		if raw != "" {
			h.RepoRemoteHash = hashRemoteURL(raw)
		}
	}

	// Commits in the window
	sinceStr := since.UTC().Format(time.RFC3339)
	untilStr := until.UTC().Format(time.RFC3339)
	// %h = short hash, one per line
	if out, err := gitCmd(workspacePath, "log",
		"--format=%h",
		fmt.Sprintf("--after=%s", sinceStr),
		fmt.Sprintf("--before=%s", untilStr),
	); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				h.CommitSHAs = append(h.CommitSHAs, line)
			}
		}
	}

	return h
}

// hashRemoteURL normalizes the remote URL and returns its sha256 hex digest.
// Normalization: lowercase, strip trailing ".git", unify git@host:org/repo
// into host/org/repo so ssh and https remotes for the same repo get the same hash.
func hashRemoteURL(raw string) string {
	u := strings.ToLower(strings.TrimSpace(raw))
	// Strip trailing .git
	u = strings.TrimSuffix(u, ".git")
	// Normalize ssh git@github.com:org/repo -> github.com/org/repo
	if strings.HasPrefix(u, "git@") {
		u = strings.TrimPrefix(u, "git@")
		u = strings.Replace(u, ":", "/", 1)
	}
	// Strip https:// or http:// scheme
	for _, pfx := range []string{"https://", "http://"} {
		if strings.HasPrefix(u, pfx) {
			u = strings.TrimPrefix(u, pfx)
			break
		}
	}
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:])
}

// gitCmd runs git -C workspacePath with the given args and returns stdout.
// Returns an error if git exits non-zero or is not found.
func gitCmd(workspacePath string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", workspacePath}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
