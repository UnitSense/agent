package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeFetcher struct {
	path string
	err  error
}

func (f fakeFetcher) Fetch(version string) (string, error) { return f.path, f.err }

type fakeChecker struct{ err error }

func (f fakeChecker) Check(path string) error { return f.err }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyNoUpdateWhenVersionsMatch(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "unitsense-agent")
	writeFile(t, bin, "current")

	out := Apply("1.4.2", "1.4.2", bin, fakeFetcher{}, fakeChecker{})

	if out.Attempted {
		t.Fatalf("expected no attempt when versions match, got %+v", out)
	}
	if got := readFile(t, bin); got != "current" {
		t.Fatalf("binary modified when no update was needed, got %q", got)
	}
}

func TestApplySwapsAndReportsOkOnHealthyUpdate(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "unitsense-agent")
	writeFile(t, bin, "old-binary")
	newBin := filepath.Join(dir, "downloaded-new")
	writeFile(t, newBin, "new-binary")

	out := Apply("1.4.2", "1.5.0", bin, fakeFetcher{path: newBin}, fakeChecker{})

	if !out.Attempted || out.Outcome != "ok" {
		t.Fatalf("expected successful update, got %+v", out)
	}
	if out.FromVersion != "1.4.2" || out.ToVersion != "1.5.0" {
		t.Fatalf("version fields wrong: %+v", out)
	}
	if got := readFile(t, bin); got != "new-binary" {
		t.Fatalf("running binary not swapped, got %q", got)
	}
	if got := readFile(t, bin+".prev"); got != "old-binary" {
		t.Fatalf("previous binary not kept as backup, got %q", got)
	}
}

func TestApplyLeavesCurrentBinaryUntouchedOnFetchFailure(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "unitsense-agent")
	writeFile(t, bin, "old-binary")

	out := Apply("1.4.2", "1.5.0", bin, fakeFetcher{err: errors.New("cosign verification failed")}, fakeChecker{})

	if !out.Attempted || out.Outcome != "fail" {
		t.Fatalf("expected failed attempt, got %+v", out)
	}
	if got := readFile(t, bin); got != "old-binary" {
		t.Fatalf("binary was touched despite fetch failure: %q", got)
	}
	if _, err := os.Stat(bin + ".prev"); !os.IsNotExist(err) {
		t.Fatalf("backup file should not exist after a fetch failure")
	}
}

func TestApplyRollsBackWhenSelfCheckFails(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "unitsense-agent")
	writeFile(t, bin, "old-binary")
	newBin := filepath.Join(dir, "downloaded-new")
	writeFile(t, newBin, "broken-binary")

	out := Apply("1.4.2", "1.5.0", bin, fakeFetcher{path: newBin}, fakeChecker{err: errors.New("exit status 1")})

	if !out.Attempted || out.Outcome != "fail" {
		t.Fatalf("expected failed attempt, got %+v", out)
	}
	if got := readFile(t, bin); got != "old-binary" {
		t.Fatalf("expected automatic rollback to old binary, got %q", got)
	}
	if _, err := os.Stat(bin + ".prev"); !os.IsNotExist(err) {
		t.Fatalf("backup file should be consumed by the rollback, not left behind")
	}
}
