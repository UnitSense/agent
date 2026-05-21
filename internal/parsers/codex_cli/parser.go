package codex_cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnitSense/agent/internal/parsers"
)

const ParserVersionConst = "codex-cli-parser-0.1.0"

type Parser struct {
	rootDir string
}

func NewParser(rootDir string) *Parser {
	return &Parser{rootDir: rootDir}
}

func (p *Parser) Provider() string      { return "agent_codex_cli" }
func (p *Parser) Tool() string          { return "codex_cli" }
func (p *Parser) ParserVersion() string { return ParserVersionConst }

type rawEvent struct {
	Type      string    `json:"type"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
	Model     string    `json:"model,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	Success   bool      `json:"success,omitempty"`
}

func (p *Parser) Aggregate(window parsers.TimeWindow) ([]parsers.DayAggregate, error) {
	if _, err := os.Stat(p.rootDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	type bucket struct {
		tools, successfulTools int
		models                 map[string]int
		sessionTimes           map[string]struct{ min, max time.Time }
	}
	byDate := map[string]*bucket{}

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
			b := byDate[dateStr]
			if b == nil {
				b = &bucket{models: map[string]int{}, sessionTimes: map[string]struct{ min, max time.Time }{}}
				byDate[dateStr] = b
			}
			if ev.SessionID != "" {
				st := b.sessionTimes[ev.SessionID]
				if st.min.IsZero() || ev.Timestamp.Before(st.min) {
					st.min = ev.Timestamp
				}
				if ev.Timestamp.After(st.max) {
					st.max = ev.Timestamp
				}
				b.sessionTimes[ev.SessionID] = st
			}
			switch ev.Type {
			case "assistant":
				if ev.Model != "" {
					b.models[ev.Model]++
				}
			case "tool_call":
				b.tools++
			case "tool_result":
				if ev.Success {
					b.successfulTools++
				}
			}
		}
		return nil
	})

	var out []parsers.DayAggregate
	for dateStr, b := range byDate {
		var elapsedMin int
		for _, st := range b.sessionTimes {
			d := st.max.Sub(st.min)
			if d > 0 {
				elapsedMin += int(d.Minutes())
			}
		}
		d, _ := time.Parse("2006-01-02", dateStr)
		agg := parsers.DayAggregate{
			Date:         d,
			DateString:   dateStr,
			SessionCount: len(b.sessionTimes),
			ModelsUsed:   b.models,
			MetricProvenance: map[string]string{
				"elapsed_method":         "max_minus_min_per_session",
				"successful_tool_method": "tool_result_success_true",
			},
			CostSource: "unknown",
		}
		if elapsedMin > 0 {
			m := elapsedMin
			agg.TotalElapsedMinutes = &m
		}
		if b.tools > 0 {
			t := b.tools
			agg.ToolInvocations = &t
		}
		if b.successfulTools > 0 {
			st := b.successfulTools
			agg.SuccessfulToolInvocations = &st
		}
		out = append(out, agg)
	}
	return out, nil
}
