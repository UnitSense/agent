package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "0.1.0-dev"
	commit  = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "unitsense-agent",
	Short: "UnitSense Agent — ships AI coding session aggregates to UnitSense",
	Long: `unitsense-agent reads session JSONLs produced by AI coding tools
(Claude Code, Codex CLI), computes per-day aggregate metrics, and posts
them to UnitSense. Privacy-first by default — never ships prompts,
responses, file contents, or raw tool inputs/outputs.

For full design + privacy contract: https://github.com/UnitSense/agent`,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
