package codex_cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

const ParserVersionConst = "codex-cli-parser-0.2.0"

var (
	commitRegex  = regexp.MustCompile(`\bgit\s+commit\b`)
	prRegex      = regexp.MustCompile(`\bgh\s+pr\s+create\b`)
	modelKeyRe   = regexp.MustCompile(`[^A-Za-z0-9._/\-]+`)
)

// Parser reads Codex CLI JSONL session files from ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
type Parser struct {
	rootDir string
}

func NewParser(rootDir string) *Parser {
	return &Parser{rootDir: rootDir}
}

func (p *Parser) Provider() string      { return "agent_codex_cli" }
func (p *Parser) Tool() string          { return "codex_cli" }
func (p *Parser) ParserVersion() string { return ParserVersionConst }

// rawEvent is the top-level wrapper for every line in a Codex JSONL file.
type rawEvent struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID string `json:"id"`
}

type turnContextPayload struct {
	Model string `json:"model"`
}

type eventMsgPayload struct {
	Type     string   `json:"type"`
	Command  []string `json:"command,omitempty"`
	ExitCode *int     `json:"exit_code,omitempty"`
	Success  *bool    `json:"success,omitempty"`
}

type responseItemPayload struct {
	Type string `json:"type"`
}

func sanitizeModelKey(raw string) string {
	return modelKeyRe.ReplaceAllString(raw, "_")
}

func (p *Parser) Aggregate(window parsers.TimeWindow) ([]parsers.DayAggregate, error) {
	if _, err := os.Stat(p.rootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	type sessionTimes struct {
		min, max time.Time
	}

	type bucket struct {
		// session id -> time range (for elapsed minutes)
		sessionTimes map[string]*sessionTimes
		// session ids seen (for session count)
		sessionIDs map[string]struct{}

		toolInvocations           int
		successfulToolInvocations int
		commits                   int
		pullRequests              int
		models                    map[string]int
	}

	newBucket := func() *bucket {
		return &bucket{
			sessionTimes: map[string]*sessionTimes{},
			sessionIDs:   map[string]struct{}{},
			models:       map[string]int{},
		}
	}

	byDate := map[string]*bucket{}

	getBucket := func(dateStr string) *bucket {
		b := byDate[dateStr]
		if b == nil {
			b = newBucket()
			byDate[dateStr] = b
		}
		return b
	}

	// Track current session id per file (session_meta is first event in each file)
	_ = filepath.WalkDir(p.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		var currentSessionID string

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)

		for scanner.Scan() {
			var ev rawEvent
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Timestamp.IsZero() || ev.Timestamp.Before(window.From) || !ev.Timestamp.Before(window.To) {
				continue
			}

			dateStr := ev.Timestamp.UTC().Format("2006-01-02")
			b := getBucket(dateStr)

			// Track min/max for elapsed time using the current session id
			if currentSessionID != "" {
				st := b.sessionTimes[currentSessionID]
				if st == nil {
					st = &sessionTimes{}
					b.sessionTimes[currentSessionID] = st
				}
				if st.min.IsZero() || ev.Timestamp.Before(st.min) {
					st.min = ev.Timestamp
				}
				if ev.Timestamp.After(st.max) {
					st.max = ev.Timestamp
				}
			}

			switch ev.Type {
			case "session_meta":
				var sm sessionMetaPayload
				if err := json.Unmarshal(ev.Payload, &sm); err == nil && sm.ID != "" {
					currentSessionID = sm.ID
					b.sessionIDs[sm.ID] = struct{}{}
					// Initialize session times for this date
					if b.sessionTimes[sm.ID] == nil {
						b.sessionTimes[sm.ID] = &sessionTimes{}
					}
					st := b.sessionTimes[sm.ID]
					if st.min.IsZero() || ev.Timestamp.Before(st.min) {
						st.min = ev.Timestamp
					}
					if ev.Timestamp.After(st.max) {
						st.max = ev.Timestamp
					}
				}

			case "turn_context":
				var tc turnContextPayload
				if err := json.Unmarshal(ev.Payload, &tc); err == nil && tc.Model != "" {
					key := sanitizeModelKey(tc.Model)
					b.models[key]++
				}

			case "response_item":
				var ri responseItemPayload
				if err := json.Unmarshal(ev.Payload, &ri); err == nil {
					if ri.Type == "function_call" || ri.Type == "custom_tool_call" {
						b.toolInvocations++
					}
				}

			case "event_msg":
				var em eventMsgPayload
				if err := json.Unmarshal(ev.Payload, &em); err != nil {
					continue
				}
				switch em.Type {
				case "exec_command_end":
					exitOK := em.ExitCode != nil && *em.ExitCode == 0
					joined := strings.Join(em.Command, " ")

					if exitOK {
						b.successfulToolInvocations++
					}
					// Count commits and PRs only for successful commands (exit_code == 0)
					if exitOK {
						if commitRegex.MatchString(joined) {
							b.commits++
						}
						if prRegex.MatchString(joined) {
							b.pullRequests++
						}
					}

				case "patch_apply_end":
					if em.Success != nil && *em.Success {
						b.successfulToolInvocations++
					}
				}
			}
		}
		return nil
	})

	var out []parsers.DayAggregate
	for dateStr, b := range byDate {
		var elapsedMin int
		for _, st := range b.sessionTimes {
			if !st.min.IsZero() && !st.max.IsZero() {
				d := st.max.Sub(st.min)
				if d > 0 {
					elapsedMin += int(d.Minutes())
				}
			}
		}

		dt, _ := time.Parse("2006-01-02", dateStr)
		agg := parsers.DayAggregate{
			Date:         dt,
			DateString:   dateStr,
			SessionCount: len(b.sessionIDs),
			ModelsUsed:   b.models,
			MetricProvenance: map[string]string{
				"elapsed_method":         "max_minus_min_per_session",
				"successful_tool_method": "exec_command_end.exit_code==0 + patch_apply_end.success==true",
				"commit_method":          "exec_command_end exit_code==0 matching git commit regex",
				"pr_method":              "exec_command_end exit_code==0 matching gh pr create regex",
			},
			CostSource: "unknown",
		}
		if elapsedMin > 0 {
			m := elapsedMin
			agg.TotalElapsedMinutes = &m
		}
		if b.toolInvocations > 0 {
			t := b.toolInvocations
			agg.ToolInvocations = &t
		}
		if b.successfulToolInvocations > 0 {
			st := b.successfulToolInvocations
			agg.SuccessfulToolInvocations = &st
		}
		if b.commits > 0 {
			c := b.commits
			agg.Commits = &c
		}
		if b.pullRequests > 0 {
			pr := b.pullRequests
			agg.PullRequests = &pr
		}
		out = append(out, agg)
	}
	return out, nil
}
