package runlog

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFormatOmitsEmptyLatestDateAndDetail(t *testing.T) {
	e := Entry{
		Time:      time.Date(2026, 11, 9, 10, 0, 3, 0, time.UTC),
		Provider:  "claude_code",
		Outcome:   OutcomeOK,
		Found:     12,
		Sent:      12,
		Confirmed: 12,
	}
	got := Format(e)
	want := "2026-11-09T10:00:03Z claude_code ok found=12 sent=12 confirmed=12"
	if got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestFormatIncludesLatestDateAndDetailWhenSet(t *testing.T) {
	e := Entry{
		Time:       time.Date(2026, 11, 9, 10, 10, 4, 0, time.UTC),
		Provider:   "codex_cli",
		Outcome:    OutcomeFail,
		Found:      8,
		Sent:       8,
		Confirmed:  0,
		LatestDate: "2026-11-01",
		Detail:     "server 500",
	}
	got := Format(e)
	want := `2026-11-09T10:10:04Z codex_cli fail found=8 sent=8 confirmed=0 latest=2026-11-01 detail="server 500"`
	if got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

// TestFormatQuotesDetailSafely confirms a detail string containing a quote
// or newline (e.g. a raw server error body) can't break the line format --
// %q escapes it, so the entry stays exactly one line and the quoting is
// unambiguous to a reader or a future parser.
func TestFormatQuotesDetailSafely(t *testing.T) {
	e := Entry{
		Time:     time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		Provider: "codex_cli",
		Outcome:  OutcomeFail,
		Detail:   "HTTP 400: {\"error\":\"bad\"}\nextra line",
	}
	got := Format(e)
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("Format() produced more than one line: %q", got)
	}
	if !strings.Contains(got, `detail="HTTP 400: {\"error\":\"bad\"}\nextra line"`) {
		t.Errorf("Format() = %q, detail not safely quoted", got)
	}
}

func TestAppendCreatesFileAndDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "run.log")

	e := Entry{Time: time.Now(), Provider: "claude_code", Outcome: OutcomeOK}
	if err := Append(path, e); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	lines, err := ReadLast(path, 10)
	if err != nil {
		t.Fatalf("ReadLast() error = %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("ReadLast() returned %d lines, want 1", len(lines))
	}
}

func TestAppendAddsToExistingLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")

	for i := 0; i < 3; i++ {
		e := Entry{Time: time.Now(), Provider: "claude_code", Outcome: OutcomeOK, Found: i}
		if err := Append(path, e); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	lines, err := ReadLast(path, 10)
	if err != nil {
		t.Fatalf("ReadLast() error = %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("ReadLast() returned %d lines, want 3", len(lines))
	}
	// Oldest first, so the last line written is the last line read back.
	if !strings.Contains(lines[2], "found=2") {
		t.Errorf("last line = %q, want it to contain found=2", lines[2])
	}
}

// TestAppendTrimsToMaxLines confirms the diary can't grow without bound --
// writing more entries than the cap keeps only the most recent ones,
// dropping the oldest first. Uses a small injected cap (not the real
// MaxLines=1000) so the test isn't O(n^2) over a thousand-line file.
func TestAppendTrimsToMaxLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")

	const limit = 20
	total := limit + 10
	for i := 0; i < total; i++ {
		e := Entry{Time: time.Now(), Provider: "claude_code", Outcome: OutcomeOK, Found: i}
		if err := appendCapped(path, e, limit); err != nil {
			t.Fatalf("appendCapped() error at i=%d = %v", i, err)
		}
	}

	lines, err := ReadLast(path, limit+100)
	if err != nil {
		t.Fatalf("ReadLast() error = %v", err)
	}
	if len(lines) != limit {
		t.Fatalf("got %d lines, want exactly limit=%d", len(lines), limit)
	}
	// The oldest 10 entries (found=0..9) must have been dropped; the first
	// surviving entry is found=10.
	if !strings.Contains(lines[0], "found=10") {
		t.Errorf("first surviving line = %q, want it to contain found=10", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "found="+strconv.Itoa(total-1)) {
		t.Errorf("last line = %q, want it to contain found=%d", lines[len(lines)-1], total-1)
	}
}

// TestAppendRespectsRealMaxLines is a single, light check that Append
// (the public entry point) really is wired to the package's real MaxLines
// constant, without the O(n^2) cost of writing that many lines in the test
// above.
func TestAppendRespectsRealMaxLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")
	preexisting := make([]string, MaxLines)
	for i := range preexisting {
		preexisting[i] = Format(Entry{Time: time.Now(), Provider: "claude_code", Outcome: OutcomeOK, Found: i})
	}
	if err := os.WriteFile(path, []byte(strings.Join(preexisting, "\n")+"\n"), 0600); err != nil {
		t.Fatalf("seed write error = %v", err)
	}

	if err := Append(path, Entry{Time: time.Now(), Provider: "claude_code", Outcome: OutcomeOK, Found: 999999}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	lines, err := ReadLast(path, MaxLines+10)
	if err != nil {
		t.Fatalf("ReadLast() error = %v", err)
	}
	if len(lines) != MaxLines {
		t.Fatalf("got %d lines, want exactly MaxLines=%d", len(lines), MaxLines)
	}
	if !strings.Contains(lines[len(lines)-1], "found=999999") {
		t.Errorf("last line = %q, want the just-appended entry", lines[len(lines)-1])
	}
}

// TestParseLineRoundTripsFormat confirms ParseLine reverses Format exactly
// for every field, including an entry with no LatestDate/Detail at all.
func TestParseLineRoundTripsFormat(t *testing.T) {
	e := Entry{
		Time:       time.Date(2026, 11, 9, 10, 10, 4, 0, time.UTC),
		Provider:   "codex_cli",
		Outcome:    OutcomeFail,
		Found:      8,
		Sent:       8,
		Confirmed:  0,
		LatestDate: "2026-11-01",
		Detail:     "server 500",
	}
	got, ok := ParseLine(Format(e))
	if !ok {
		t.Fatalf("ParseLine() returned ok=false for a line Format() itself wrote")
	}
	if !got.Time.Equal(e.Time) || got.Provider != e.Provider || got.Outcome != e.Outcome ||
		got.Found != e.Found || got.Sent != e.Sent || got.Confirmed != e.Confirmed ||
		got.LatestDate != e.LatestDate || got.Detail != e.Detail {
		t.Errorf("ParseLine(Format(e)) = %+v, want %+v", got, e)
	}
}

// TestParseLineRoundTripsDetailWithSpacesAndQuotes confirms a detail string
// containing spaces and an embedded quote survives Format -> ParseLine,
// since that's exactly the shape of a raw server error body.
func TestParseLineRoundTripsDetailWithSpacesAndQuotes(t *testing.T) {
	e := Entry{
		Time:     time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		Provider: "codex_cli",
		Outcome:  OutcomeFail,
		Detail:   `HTTP 400: {"error":"bad"}`,
	}
	got, ok := ParseLine(Format(e))
	if !ok {
		t.Fatalf("ParseLine() returned ok=false")
	}
	if got.Detail != e.Detail {
		t.Errorf("Detail = %q, want %q", got.Detail, e.Detail)
	}
}

func TestParseLineRejectsGarbage(t *testing.T) {
	if _, ok := ParseLine("not a diary line at all"); ok {
		t.Errorf("ParseLine() = ok=true for garbage input, want false")
	}
}

// TestLatestPerProviderPicksMostRecentEntryPerProvider confirms that when
// the same provider appears more than once in the tail (consecutive runs),
// the newest one wins -- not the first or last by file position, by Time.
func TestLatestPerProviderPicksMostRecentEntryPerProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")

	base := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Time: base, Provider: "claude_code", Outcome: OutcomeOK, Found: 1, Sent: 1, Confirmed: 1},
		{Time: base, Provider: "codex_cli", Outcome: OutcomeFail, Detail: "first failure"},
		{Time: base.Add(10 * time.Minute), Provider: "claude_code", Outcome: OutcomeOK, Found: 2, Sent: 2, Confirmed: 2},
		{Time: base.Add(10 * time.Minute), Provider: "codex_cli", Outcome: OutcomeOK, Detail: "no new activity"},
	}
	for _, e := range entries {
		if err := Append(path, e); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	latest, err := LatestPerProvider(path, 10)
	if err != nil {
		t.Fatalf("LatestPerProvider() error = %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("got %d providers, want 2: %+v", len(latest), latest)
	}
	if latest["claude_code"].Found != 2 {
		t.Errorf("claude_code.Found = %d, want 2 (the later entry)", latest["claude_code"].Found)
	}
	// The most recent codex_cli entry is "ok" -- a prior failure must not
	// linger once a later run succeeds.
	if latest["codex_cli"].Outcome != OutcomeOK {
		t.Errorf("codex_cli.Outcome = %q, want %q (the later, successful entry)", latest["codex_cli"].Outcome, OutcomeOK)
	}
}

func TestLatestPerProviderSkipsUnparseableLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")
	if err := os.WriteFile(path, []byte("garbage line\nmore garbage\n"), 0600); err != nil {
		t.Fatalf("seed write error = %v", err)
	}
	latest, err := LatestPerProvider(path, 10)
	if err != nil {
		t.Fatalf("LatestPerProvider() error = %v", err)
	}
	if len(latest) != 0 {
		t.Errorf("got %d providers from garbage input, want 0: %+v", len(latest), latest)
	}
}

func TestReadLastReturnsNilWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.log")

	lines, err := ReadLast(path, 10)
	if err != nil {
		t.Fatalf("ReadLast() error = %v, want nil", err)
	}
	if lines != nil {
		t.Errorf("ReadLast() = %v, want nil", lines)
	}
}

// TestAppendSetsPrivateFilePermissions confirms the diary file isn't created
// world-readable, matching the existing config/state files' 0600 convention
// -- it can contain machine IDs and server error bodies. Windows doesn't map
// Unix permission bits (see internal/config/permissions_windows.go), so this
// only means anything on Unix.
func TestAppendSetsPrivateFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not map Unix permission bits (see internal/config/permissions_windows.go)")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")

	if err := Append(path, Entry{Time: time.Now(), Provider: "claude_code", Outcome: OutcomeOK}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}
