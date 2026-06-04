package githints

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestHashRemoteURL(t *testing.T) {
	cases := []struct {
		a, b string
	}{
		// SSH and HTTPS for the same repo should hash identically
		{"git@github.com:acme/myrepo.git", "https://github.com/acme/myrepo"},
		// Trailing .git stripped
		{"https://github.com/acme/myrepo.git", "https://github.com/acme/myrepo"},
		// Case-insensitive
		{"https://GitHub.com/Acme/MyRepo", "https://github.com/acme/myrepo"},
	}
	for _, c := range cases {
		ha := hashRemoteURL(c.a)
		hb := hashRemoteURL(c.b)
		if ha != hb {
			t.Errorf("hash(%q)=%s != hash(%q)=%s", c.a, ha, c.b, hb)
		}
		if len(ha) != 64 {
			t.Errorf("expected 64-char hex, got %d", len(ha))
		}
	}
}

func TestExtractEmptyPath(t *testing.T) {
	h := Extract("", time.Now().Add(-1*time.Hour), time.Now())
	if h.RepoRemoteHash != "" || h.BranchName != "" || len(h.CommitSHAs) != 0 {
		t.Errorf("expected empty Hints for empty path, got %+v", h)
	}
}

func TestExtractNonRepo(t *testing.T) {
	dir := t.TempDir()
	h := Extract(dir, time.Now().Add(-1*time.Hour), time.Now())
	// Not a repo → all fields empty, no panic
	if h.RepoRemoteHash != "" || h.BranchName != "" || len(h.CommitSHAs) != 0 {
		t.Errorf("expected empty Hints for non-repo dir, got %+v", h)
	}
}

// TestExtractRealRepo creates a temp git repo with a commit and verifies that
// Extract returns branch name and the commit SHA.
func TestExtractRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", dir)
	run("-C", dir, "checkout", "-b", "feat/test-branch")

	// Add a file and commit within our time window
	f := dir + "/hello.txt"
	os.WriteFile(f, []byte("hello"), 0644)
	run("-C", dir, "add", "hello.txt")

	// Use GIT_COMMITTER_DATE / GIT_AUTHOR_DATE to pin the commit inside the window
	since := time.Now().Add(-2 * time.Hour)
	until := time.Now().Add(1 * time.Hour)
	commitTime := time.Now().UTC().Format(time.RFC3339)

	cmd := exec.Command("git", "-C", dir, "commit", "-m", "initial")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE="+commitTime,
		"GIT_COMMITTER_DATE="+commitTime,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	h := Extract(dir, since, until)

	if h.BranchName != "feat/test-branch" {
		t.Errorf("BranchName = %q, want feat/test-branch", h.BranchName)
	}
	// No remote configured → hash should be empty
	if h.RepoRemoteHash != "" {
		t.Errorf("RepoRemoteHash = %q, want empty (no remote)", h.RepoRemoteHash)
	}
	// Should have exactly 1 commit SHA
	if len(h.CommitSHAs) != 1 {
		t.Errorf("CommitSHAs = %v, want 1 entry", h.CommitSHAs)
	}
	if len(h.CommitSHAs) > 0 && len(h.CommitSHAs[0]) == 0 {
		t.Error("CommitSHA is empty string")
	}

	// Now add a fake remote and verify hashing
	run("-C", dir, "remote", "add", "origin", "git@github.com:acme/testrepo.git")
	h2 := Extract(dir, since, until)
	if h2.RepoRemoteHash == "" {
		t.Error("RepoRemoteHash should be non-empty after adding remote")
	}
	expected := hashRemoteURL("git@github.com:acme/testrepo.git")
	if h2.RepoRemoteHash != expected {
		t.Errorf("RepoRemoteHash = %q, want %q", h2.RepoRemoteHash, expected)
	}
	// Verify it matches the https equivalent
	httpsHash := hashRemoteURL("https://github.com/acme/testrepo")
	if h2.RepoRemoteHash != httpsHash {
		t.Errorf("ssh and https hashes differ: %q vs %q", h2.RepoRemoteHash, httpsHash)
	}

	// Commits outside window should not appear
	h3 := Extract(dir, time.Now().Add(-10*time.Minute), time.Now().Add(-5*time.Minute))
	if strings.Join(h3.CommitSHAs, "") != "" {
		t.Errorf("expected no commits outside window, got %v", h3.CommitSHAs)
	}
}
