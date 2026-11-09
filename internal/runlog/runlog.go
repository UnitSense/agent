// Package runlog keeps a small, append-only local diary of what each agent
// run actually did, per provider.
//
// This exists because launchd, systemd and Task Scheduler disagree on
// whether a background job's own output goes anywhere a person could find
// it: a failing run can leave zero trace on two of the three platforms this
// agent ships for. Writing the record ourselves, in one place, the same way
// on every OS, means a support conversation can start from "send us
// run.log" instead of "please re-run this by hand while we watch."
//
// A single number ("found 0 sessions") can't tell a quiet developer apart
// from a sync that's silently losing data, so each entry carries three:
// how much local activity the parser found, how much of that was sent, and
// how much the server actually confirmed it accepted. Idle and broken look
// the same on "found" alone; they look different once "sent" and
// "confirmed" are next to it.
package runlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxLines caps the diary at roughly 3-4 days of history for a two-provider
// setup on the default 10-minute schedule (6 runs/hour x 2 providers x ~36h
// = ~430 lines), with headroom. The file is plain text and small even at
// this size, so there's no reason to trim tighter.
const MaxLines = 1000

// Entry is one provider's outcome for one run.
type Entry struct {
	Time time.Time
	// Provider is the parser's own identifier, e.g. "claude_code", "codex_cli".
	Provider string
	// Outcome is "ok" or "fail". "ok" covers a quiet run (nothing local to
	// report) just as much as a run that synced data -- "fail" is reserved
	// for an actual error (parse failure, network error, non-2xx response).
	Outcome string
	// Found is how many day-aggregates the parser found locally in the
	// run's time window.
	Found int
	// Sent is how many of those were included in the POST to the server.
	// Equal to Found unless nothing was found, in which case no request is
	// made at all and Sent is 0.
	Sent int
	// Confirmed is accepted_count from the server's response. Only
	// meaningful when Outcome is "ok" and Sent > 0; left at 0 otherwise.
	Confirmed int
	// LatestDate is the most recent YYYY-MM-DD among the aggregates Found
	// covered, or "" if Found is 0. A diary that shows the same LatestDate
	// run after run -- rather than it advancing -- is the signature of data
	// that looks like it's syncing but never actually moves forward.
	LatestDate string
	// Detail is a short free-text reason: an error message on failure, or a
	// one-line note ("no new activity", "dry run") when there's nothing
	// else to explain.
	Detail string
}

const (
	OutcomeOK   = "ok"
	OutcomeFail = "fail"
)

// Format renders e as one diary line. Fields are fixed-order key=value pairs
// so the file stays greppable; LatestDate and Detail are omitted when empty
// rather than printed blank.
func Format(e Entry) string {
	parts := []string{
		e.Time.UTC().Format(time.RFC3339),
		e.Provider,
		e.Outcome,
		"found=" + strconv.Itoa(e.Found),
		"sent=" + strconv.Itoa(e.Sent),
		"confirmed=" + strconv.Itoa(e.Confirmed),
	}
	if e.LatestDate != "" {
		parts = append(parts, "latest="+e.LatestDate)
	}
	if e.Detail != "" {
		parts = append(parts, fmt.Sprintf("detail=%q", e.Detail))
	}
	return strings.Join(parts, " ")
}

// Append writes e to path as a new line, trimming the file down to MaxLines
// (keeping the most recent) in the same write. Creates the file and its
// parent directory if neither exists yet.
func Append(path string, e Entry) error {
	return appendCapped(path, e, MaxLines)
}

// appendCapped is Append with an injectable cap, so tests can exercise
// trimming without writing (and re-reading) a MaxLines-sized file on every
// one of many iterations.
func appendCapped(path string, e Entry, max int) error {
	existing, _ := os.ReadFile(path)

	lines := nonEmptyLines(string(existing))
	lines = append(lines, Format(e))
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

// ReadLast returns the most recent n lines in path, oldest first, or none if
// the file doesn't exist yet.
func ReadLast(path string, n int) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := nonEmptyLines(string(b))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
