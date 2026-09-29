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
	"github.com/UnitSense/agent/internal/schedule"
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

	statePath := filepath.Join(filepath.Dir(cfgPath), "state.json")
	st, _ := state.Load(statePath)

	window, err := time.ParseDuration(runWindow)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	from := now.Add(-window)
	// Never leave a gap: if the previous run succeeded, always look back at
	// least as far as that point, even if it's further than the default
	// window. Otherwise a session spanning longer than `window` can fall
	// partly outside this run's aggregation and silently regress stored
	// totals via the day-row upsert on the dashboard side.
	if st.LastRunStatus == "succeeded" && st.LastRunAt != "" {
		if lastRun, perr := time.Parse(time.RFC3339, st.LastRunAt); perr == nil && lastRun.Before(from) {
			from = lastRun
		}
	}
	tw := parsers.TimeWindow{From: from, To: now.Add(time.Hour)}

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
	start := time.Now()

	// Check in before doing any real work: in auto-sync mode this just
	// carries the tenant's configured interval (as before); in manual mode
	// it tells us whether a "Sync now" request is actually pending, and if
	// not, this run does nothing further -- the whole point of manual mode
	// is a real sync only happens when explicitly requested, not on every
	// scheduled tick.
	if !runDry {
		ci, ciErr := cl.CheckIn(client.CheckInRequest{AgentVersion: Version})
		if ciErr != nil {
			fmt.Fprintf(os.Stderr, "check-in: %v\n", ciErr)
		} else {
			reconcileSchedule(st, ci.RecommendedSyncIntervalMinutes)
			if !ci.ShouldSync {
				fmt.Println("auto sync is off — no sync requested, skipping")
				st.LastRunDurationMS = int(time.Since(start).Milliseconds())
				st.LastRunStatus = "succeeded"
				st.LastError = ""
				st.ConsecutiveFailures = 0
				_ = state.Save(statePath, st)
				return nil
			}
		}
	}

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

		// Subscription quota snapshot (optional capability). Only parsers whose
		// source files carry it (Codex) or can estimate it (Claude) implement
		// RateLimitReader; the rest are skipped.
		if rl, ok := p.(parsers.RateLimitReader); ok {
			snap, rErr := rl.LatestRateLimit(tw)
			if rErr != nil {
				fmt.Fprintf(os.Stderr, "parser %s rate-limit error: %v\n", p.Tool(), rErr)
			} else if snap != nil {
				rlReq := client.RateLimitsRequest{
					RequestID:     uuid.New(),
					AgentVersion:  Version,
					ParserVersion: p.ParserVersion(),
					Provider:      p.Provider(),
					Snapshots:     []map[string]any{rateLimitToPayload(snap, cfg.MachineID.String())},
				}
				if runDry {
					fmt.Printf("--- DRY: %s rate-limits ---\n", p.Provider())
					fmt.Printf("request_id=%s plan=%s estimated=%t\n", rlReq.RequestID, snap.PlanType, snap.Estimated)
				} else if rlResp, err := cl.PostRateLimits(rlReq); err != nil {
					fmt.Fprintf(os.Stderr, "post rate-limits (%s): %v\n", p.Provider(), err)
					if firstErr == nil {
						firstErr = err
					}
				} else {
					fmt.Printf("%s: %d rate-limit snapshot accepted (run=%s)\n", p.Provider(), rlResp.AcceptedCount, rlResp.RunID)
				}
			}
		}
	}

	duration := time.Since(start)
	if firstErr == nil && !runDry {
		// Only advance the watermark on a real, fully successful run. A
		// --dry run never posts anything, so it must not mark the gap as
		// closed; a partial/total failure must not either, or the next
		// run's window would start past data that never reached the server.
		st.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	}
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

// reconcileSchedule updates the OS-scheduled task if the server recommends a
// different interval than what's currently installed. Only ever adjusts a
// schedule that already exists (ScheduledIntervalMinutes > 0, meaning
// `install` was run at least once) — never silently creates one from
// nothing. Mutates st in place on success.
func reconcileSchedule(st *state.State, recommendedMinutes int) {
	if recommendedMinutes <= 0 || st.ScheduledIntervalMinutes <= 0 || recommendedMinutes == st.ScheduledIntervalMinutes {
		return
	}
	bin, err := os.Executable()
	if err != nil {
		return
	}
	if err := schedule.Install(bin, time.Duration(recommendedMinutes)*time.Minute); err != nil {
		fmt.Fprintf(os.Stderr, "failed to reconcile schedule: %v\n", err)
		return
	}
	fmt.Printf("sync interval updated: %dm -> %dm\n", st.ScheduledIntervalMinutes, recommendedMinutes)
	st.ScheduledIntervalMinutes = recommendedMinutes
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
		if s.PromptCount != nil {
			ev["prompt_count"] = *s.PromptCount
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

// rateLimitToPayload converts a RateLimitSnapshot into a JSON-ready map for the
// /api/v1/agent/rate-limits endpoint. Developer attribution is resolved
// server-side from the device token; we send machine_id for provenance only.
func rateLimitToPayload(s *parsers.RateLimitSnapshot, machineID string) map[string]any {
	win := func(w *parsers.RateLimitWindow) map[string]any {
		if w == nil {
			return nil
		}
		m := map[string]any{
			"used_percent":   w.UsedPercent,
			"window_minutes": w.WindowMinutes,
		}
		if !w.ResetsAt.IsZero() {
			m["resets_at"] = w.ResetsAt.UTC().Format(time.RFC3339)
		}
		return m
	}
	return map[string]any{
		"machine_id":  machineID,
		"plan_type":   s.PlanType,
		"captured_at": s.CapturedAt.UTC().Format(time.RFC3339),
		"estimated":   s.Estimated,
		"primary":     win(s.Primary),
		"secondary":   win(s.Secondary),
	}
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
		if a.PromptCount != nil {
			ev["prompt_count"] = *a.PromptCount
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
