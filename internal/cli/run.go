package cli

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/UnitSense/agent/internal/client"
	"github.com/UnitSense/agent/internal/config"
	"github.com/UnitSense/agent/internal/parsers"
	"github.com/UnitSense/agent/internal/parsers/claude_code"
	"github.com/UnitSense/agent/internal/parsers/codex_cli"
	"github.com/UnitSense/agent/internal/state"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var (
	runWindow   string
	runDry      bool
	runNoJitter bool
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "One-shot sync: parse, aggregate, post",
	RunE:  runRun,
}

func init() {
	runCmd.Flags().StringVar(&runWindow, "window", "24h", "Lookback window (e.g. 24h, 48h)")
	runCmd.Flags().BoolVar(&runDry, "dry", false, "Print payload but do not POST")
	runCmd.Flags().BoolVar(&runNoJitter, "no-jitter", false, "Skip the random startup delay")
	RegisterCommand(runCmd)
}

func runRun(cmd *cobra.Command, args []string) error {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if !runNoJitter && cfg.JitterSeconds > 0 {
		j := time.Duration(rand.Intn(cfg.JitterSeconds+1)) * time.Second
		fmt.Fprintf(os.Stderr, "jittering %s\n", j)
		time.Sleep(j)
	}

	window, err := time.ParseDuration(runWindow)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tw := parsers.TimeWindow{From: now.Add(-window), To: now.Add(time.Hour)}

	home, _ := os.UserHomeDir()
	parserList := []parsers.Parser{}
	for _, p := range cfg.Providers {
		switch p {
		case "claude_code":
			parserList = append(parserList, claude_code.NewParser(filepath.Join(home, ".claude", "projects")))
		case "codex_cli":
			parserList = append(parserList, codex_cli.NewParser(filepath.Join(home, ".codex", "sessions")))
		}
	}
	if len(parserList) == 0 {
		return errors.New("no providers enabled in config")
	}

	cl := client.New(cfg.ServerURL, cfg.DeviceToken)
	statePath := filepath.Join(filepath.Dir(cfgPath), "state.json")
	st, _ := state.Load(statePath)
	start := time.Now()
	totalSent := 0
	var firstErr error

	for _, p := range parserList {
		aggs, err := p.Aggregate(tw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parser %s error: %v\n", p.Tool(), err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(aggs) == 0 {
			continue
		}
		events := aggregatesToEvents(aggs)
		req := client.EventsRequest{
			RequestID:     uuid.New(),
			AgentVersion:  Version,
			ParserVersion: p.ParserVersion(),
			Provider:      p.Provider(),
			Events:        events,
		}
		if runDry {
			fmt.Printf("--- DRY: %s ---\n", p.Provider())
			fmt.Printf("request_id=%s events=%d\n", req.RequestID, len(events))
			continue
		}
		resp, err := cl.PostEvents(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "post events (%s): %v\n", p.Provider(), err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		totalSent += resp.AcceptedCount
		fmt.Printf("%s: %d events accepted (run=%s)\n", p.Provider(), resp.AcceptedCount, resp.RunID)
	}

	duration := time.Since(start)
	st.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	st.LastRunDurationMS = int(duration.Milliseconds())
	st.LastRunEventsSent = totalSent
	if firstErr != nil {
		st.LastRunStatus = "failed"
		st.LastError = firstErr.Error()
		st.ConsecutiveFailures++
	} else {
		st.LastRunStatus = "succeeded"
		st.LastError = ""
		st.ConsecutiveFailures = 0
	}
	_ = state.Save(statePath, st)
	return firstErr
}

func aggregatesToEvents(aggs []parsers.DayAggregate) []map[string]any {
	out := make([]map[string]any, 0, len(aggs))
	for _, a := range aggs {
		ev := map[string]any{
			"date":          a.DateString,
			"session_count": a.SessionCount,
		}
		if a.TotalElapsedMinutes != nil {
			ev["total_elapsed_minutes"] = *a.TotalElapsedMinutes
		}
		if a.LinesAdded != nil {
			ev["lines_added"] = *a.LinesAdded
		}
		if a.LinesRemoved != nil {
			ev["lines_removed"] = *a.LinesRemoved
		}
		if a.Commits != nil {
			ev["commits"] = *a.Commits
		}
		if a.PullRequests != nil {
			ev["pull_requests"] = *a.PullRequests
		}
		if a.ToolInvocations != nil {
			ev["tool_invocations"] = *a.ToolInvocations
		}
		if a.SuccessfulToolInvocations != nil {
			ev["successful_tool_invocations"] = *a.SuccessfulToolInvocations
		}
		if len(a.ModelsUsed) > 0 {
			ev["models_used"] = a.ModelsUsed
		}
		if len(a.ToolCallsByName) > 0 {
			ev["tool_calls_by_name"] = a.ToolCallsByName
		}
		if a.EstimatedCostUSD != nil {
			ev["estimated_cost_usd"] = *a.EstimatedCostUSD
		}
		if a.CostSource != "" {
			ev["cost_source"] = a.CostSource
		}
		if len(a.MetricProvenance) > 0 {
			ev["metric_provenance"] = a.MetricProvenance
		}
		out = append(out, ev)
	}
	return out
}
