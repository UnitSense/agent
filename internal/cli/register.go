package cli

import "github.com/spf13/cobra"

var Version = "0.1.0-dev"
var Commit = "unknown"

var rootCmd = &cobra.Command{
	Use:   "unitsense-agent",
	Short: "UnitSense Agent — ships AI coding session aggregates to UnitSense",
	Long: `unitsense-agent reads session JSONLs produced by AI coding tools
(Claude Code, Codex CLI), computes per-day aggregate metrics, and posts
them to UnitSense. Privacy-first by default — never ships prompts,
responses, file contents, or raw tool inputs/outputs.

For full design + privacy contract: https://github.com/UnitSense/agent`,
}

// RegisterCommand attaches a subcommand to the root.
func RegisterCommand(c *cobra.Command) { rootCmd.AddCommand(c) }

// Root returns the root cobra command (used by main).
func Root() *cobra.Command { return rootCmd }
