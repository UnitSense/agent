package cli

import (
	"crypto/sha256"
	"encoding/hex"
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
			parserList = append(parserList, claude_code.NewParserWithOptions(
				filepath.Join(home, ".claude", "projects"),
				cfg.EnableGitHints,
			))
		case "codex_cli":
			parserList = append(parserList, codex_cli.NewParserWithOptions(
				filepath.Join(home, ".codex", "sessions"),
				cfg.EnableGitHints,
			))
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
		if len(aggs) > 0 {
			events := aggregatesToEvents(aggs)
			req := client.EventsRequest{
				RequestID:     uuid.New(),
				AgentVersion:  Version,
				ParserVersion: p.ParserVersion(),
				Provider:      p.Provider(),
				Events:        events,
			}
			if runDry {
				fmt.Printf("--- DRY: %s events ---\n", p.Provider())
				fmt.Printf("request_id=%s events=%d\n", req.RequestID, len(events))
			} else {
				resp, err := cl.PostEvents(req)
				if err != nil {
					fmt.Fprintf(os.Stderr, "post events (%s): %v\n", p.Provider(), err)
					if firstErr == nil {
						firstErr = err
					}
				} else {
					totalSent += resp.AcceptedCount
					fmt.Printf("%s: %d events accepted (run=%s)\n", p.Provider(), resp.AcceptedCount, resp.RunID)
				}
			}
		}

		// Per-session emission (added in agent v0.4.0). Sessions enrich the
		// daily aggregate path; if AggregateSessions returns nothing, skip.
		sessions, sErr := p.AggregateSessions(tw)
		if sErr != nil {
			fmt.Fprintf(os.Stderr, "parser %s sessions error: %v\n", p.Tool(), sErr)
			if firstErr == nil {
				firstErr = sErr
			}
			continue
		}
		if len(sessions) == 0 {
			continue
		}
		sessEvents := sessionsToPayload(sessions, cfg.MachineID.String())
		sessReq := client.SessionsRequest{
			RequestID:        uuid.New(),
			AgentVersion:     Version,
			ParserVersion:    p.ParserVersion(),
			Provider:         p.Provider(),
			SessionSummaries: sessEvents,
		}
		if runDry {
			fmt.Printf("--- DRY: %s sessions ---\n", p.Provider())
			fmt.Printf("request_id=%s sessions=%d\n", sessReq.RequestID, len(sessEvents))
			continue
		}
		sessResp, err := cl.PostSessions(sessReq)
		if err != nil {
			fmt.Fprintf(os.Stderr, "post sessions (%s): %v\n", p.Provider(), err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		fmt.Printf("%s: %d sessions accepted (run=%s)\n", p.Provider(), sessResp.AcceptedCount, sessResp.RunID)
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

// sessionsToPayload converts SessionSummary structs into JSON-ready maps for
// the /api/v1/agent/sessions endpoint. Hashes the raw session key using the
// agent's machine_id + session_date so the server never sees raw filesystem
// session IDs (privacy contract from the session drilldown spec).
func sessionsToPayload(sessions []parsers.SessionSummary, machineID string) []map[string]any {
	out := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		hasher := sha256.New()
		hasher.Write([]byte(machineID))
		hasher.Write([]byte(":"))
		hasher.Write([]byte(s.SessionKey))
		hasher.Write([]byte(":"))
		hasher.Write([]byte(s.SessionDate.Format("2006-01-02")))
		keyHash := hex.EncodeToString(hasher.Sum(nil))

		ev := map[string]any{
			"session_key_hash": keyHash,
			"session_date":     s.SessionDate.Format("2006-01-02"),
			"started_at":       s.StartedAt.Format(time.RFC3339),
			"ended_at":         s.EndedAt.Format(time.RFC3339),
			"elapsed_minutes":  s.ElapsedMinutes,
			"models_used":      s.ModelsUsed,
			"tool_counts":      s.ToolCounts,
		}
		if s.SuccessfulToolInvocations != nil {
			ev["successful_tool_invocations"] = *s.SuccessfulToolInvocations
		}
		if s.InputTokens != nil {
			ev["input_tokens"] = *s.InputTokens
		}
		if s.OutputTokens != nil {
			ev["output_tokens"] = *s.OutputTokens
		}
		if s.CacheReadTokens != nil {
			ev["cache_read_tokens"] = *s.CacheReadTokens
		}
		if s.CacheCreationTokens != nil {
			ev["cache_creation_tokens"] = *s.CacheCreationTokens
		}
		if s.RepoRemoteHash != "" {
			ev["repo_remote_hash"] = s.RepoRemoteHash
		}
		if s.BranchName != "" {
			ev["branch_name"] = s.BranchName
		}
		if len(s.CommitSHAs) > 0 {
			ev["commit_shas"] = s.CommitSHAs
		}
		out = append(out, ev)
	}
	return out
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
		if a.InputTokens != nil {
			ev["input_tokens"] = *a.InputTokens
		}
		if a.OutputTokens != nil {
			ev["output_tokens"] = *a.OutputTokens
		}
		if a.CacheReadTokens != nil {
			ev["cache_read_tokens"] = *a.CacheReadTokens
		}
		if a.CacheCreationTokens != nil {
			ev["cache_creation_tokens"] = *a.CacheCreationTokens
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
