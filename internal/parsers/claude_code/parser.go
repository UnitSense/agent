package claude_code

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

const ParserVersionConst = "claude-code-parser-0.5.0"

type Parser struct {
	rootDir string
}

func NewParser(rootDir string) *Parser {
	return &Parser{rootDir: rootDir}
}

func (p *Parser) Provider() string      { return "agent_claude_code" }
func (p *Parser) Tool() string          { return "claude_code" }
func (p *Parser) ParserVersion() string { return ParserVersionConst }

type rawEvent struct {
	Type      string    `json:"type"`
	SessionID string    `json:"sessionId"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Model   string `json:"model,omitempty"`
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name,omitempty"`
			Input struct {
				OldString string `json:"old_string,omitempty"`
				NewString string `json:"new_string,omitempty"`
				Command   string `json:"command,omitempty"`
			} `json:"input,omitempty"`
			IsError bool `json:"is_error,omitempty"`
		} `json:"content,omitempty"`
		Usage struct {
			InputTokens              int64 `json:"input_tokens,omitempty"`
			OutputTokens             int64 `json:"output_tokens,omitempty"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
		} `json:"usage,omitempty"`
	} `json:"message,omitempty"`
}

var (
	commitRegex        = regexp.MustCompile(`(?m)^git\s+commit\b`)
	prRegex            = regexp.MustCompile(`(?m)^gh\s+pr\s+create\b`)
	invalidModelKeyRe  = regexp.MustCompile(`[^A-Za-z0-9._/\-]`)
)

// sanitizeModelKey replaces characters not allowed by the server's models_used
// key regex (/^[A-Za-z0-9._/-]+$/) with underscores, so values like
// "<synthetic>" are stored as "_synthetic_" rather than rejected.
func sanitizeModelKey(s string) string {
	return invalidModelKeyRe.ReplaceAllString(s, "_")
}

func (p *Parser) Aggregate(window parsers.TimeWindow) ([]parsers.DayAggregate, error) {
	if _, err := os.Stat(p.rootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	type dayBucket struct {
		toolInvocations           int
		successfulToolInvocations int
		linesAdded, linesRemoved  int
		commits, pullRequests     int
		models                    map[string]int
		toolsByName               map[string]int // NEW
		inputTokens               int64
		outputTokens              int64
		cacheReadTokens           int64
		cacheCreationTokens       int64
	}
	byDate := map[string]*dayBucket{}
	sessionDates := map[string]map[string]struct{ minTS, maxTS time.Time }{}

	var jsonlPaths []string
	_ = filepath.WalkDir(p.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") {
			jsonlPaths = append(jsonlPaths, path)
		}
		return nil
	})

	for _, file := range jsonlPaths {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			var ev rawEvent
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Timestamp.IsZero() {
				continue
			}
			if ev.Timestamp.Before(window.From) || !ev.Timestamp.Before(window.To) {
				continue
			}
			dateStr := ev.Timestamp.UTC().Format("2006-01-02")
			b := byDate[dateStr]
			if b == nil {
				b = &dayBucket{models: map[string]int{}, toolsByName: map[string]int{}}
				byDate[dateStr] = b
			}

			if ev.SessionID != "" {
				if sessionDates[dateStr] == nil {
					sessionDates[dateStr] = map[string]struct{ minTS, maxTS time.Time }{}
				}
				sd := sessionDates[dateStr][ev.SessionID]
				if sd.minTS.IsZero() || ev.Timestamp.Before(sd.minTS) {
					sd.minTS = ev.Timestamp
				}
				if ev.Timestamp.After(sd.maxTS) {
					sd.maxTS = ev.Timestamp
				}
				sessionDates[dateStr][ev.SessionID] = sd
			}

			switch ev.Type {
			case "assistant":
				if ev.Message.Model != "" {
					b.models[sanitizeModelKey(ev.Message.Model)]++
				}
				b.inputTokens += ev.Message.Usage.InputTokens
				b.outputTokens += ev.Message.Usage.OutputTokens
				b.cacheReadTokens += ev.Message.Usage.CacheReadInputTokens
				b.cacheCreationTokens += ev.Message.Usage.CacheCreationInputTokens
				for _, c := range ev.Message.Content {
					if c.Type == "tool_use" {
						b.toolInvocations++
						toolName := c.Name
						if toolName != "" {
							b.toolsByName[toolName]++
						}
						if c.Name == "Edit" || c.Name == "Write" {
							if c.Input.NewString != "" {
								b.linesAdded += countLines(c.Input.NewString)
							}
							if c.Input.OldString != "" {
								b.linesRemoved += countLines(c.Input.OldString)
							}
						}
						if c.Name == "Bash" {
							if commitRegex.MatchString(c.Input.Command) {
								b.commits++
							}
							if prRegex.MatchString(c.Input.Command) {
								b.pullRequests++
							}
						}
					}
				}
			case "user":
				for _, c := range ev.Message.Content {
					if c.Type == "tool_result" && !c.IsError {
						b.successfulToolInvocations++
					}
				}
			}
		}
		f.Close()
	}

	var out []parsers.DayAggregate
	for dateStr, b := range byDate {
		var totalMin int
		for _, sd := range sessionDates[dateStr] {
			diff := sd.maxTS.Sub(sd.minTS)
			if diff > 0 {
				totalMin += int(diff.Minutes())
			}
		}
		sessionCount := len(sessionDates[dateStr])
		d, _ := time.Parse("2006-01-02", dateStr)

		agg := parsers.DayAggregate{
			Date:         d,
			DateString:   dateStr,
			SessionCount: sessionCount,
			ModelsUsed:   b.models,
			MetricProvenance: map[string]string{
				"lines_method":           "edit_tool_input_estimate",
				"elapsed_method":         "max_minus_min_per_session",
				"successful_tool_method": "tool_result_no_error",
			},
			CostSource: "unknown",
		}
		if totalMin > 0 {
			agg.TotalElapsedMinutes = intPtr(totalMin)
		}
		if b.toolInvocations > 0 {
			agg.ToolInvocations = intPtr(b.toolInvocations)
		}
		if b.successfulToolInvocations > 0 {
			agg.SuccessfulToolInvocations = intPtr(b.successfulToolInvocations)
		}
		if b.linesAdded > 0 {
			agg.LinesAdded = intPtr(b.linesAdded)
		}
		if b.linesRemoved > 0 {
			agg.LinesRemoved = intPtr(b.linesRemoved)
		}
		if b.commits > 0 {
			agg.Commits = intPtr(b.commits)
		}
		if b.pullRequests > 0 {
			agg.PullRequests = intPtr(b.pullRequests)
		}
		if len(b.toolsByName) > 0 {
			agg.ToolCallsByName = b.toolsByName
		}
		if b.inputTokens > 0 {
			v := b.inputTokens
			agg.InputTokens = &v
		}
		if b.outputTokens > 0 {
			v := b.outputTokens
			agg.OutputTokens = &v
		}
		if b.cacheReadTokens > 0 {
			v := b.cacheReadTokens
			agg.CacheReadTokens = &v
		}
		if b.cacheCreationTokens > 0 {
			v := b.cacheCreationTokens
			agg.CacheCreationTokens = &v
		}
		out = append(out, agg)
	}
	return out, nil
}

func intPtr(i int) *int { return &i }

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}
