package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/UnitSense/agent/internal/config"
	"github.com/UnitSense/agent/internal/parsers"
	"github.com/UnitSense/agent/internal/parsers/claude_code"
	"github.com/UnitSense/agent/internal/parsers/codex_cli"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var testShowTenant bool

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Local dry-run: parse + aggregate + print payload (NO network)",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.DefaultPath()
		if err != nil {
			return err
		}
		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		home, _ := os.UserHomeDir()
		now := time.Now().UTC()
		tw := parsers.TimeWindow{From: now.Add(-24 * time.Hour), To: now.Add(time.Hour)}

		tenant := cfg.Tenant
		if !testShowTenant && len(tenant) > 4 {
			tenant = tenant[:2] + "***" + tenant[len(tenant)-2:]
		}
		fmt.Printf("# test mode — no network call\n")
		fmt.Printf("# server:        %s\n", cfg.ServerURL)
		fmt.Printf("# tenant:        %s\n", tenant)
		fmt.Printf("# machine_label: %s\n", cfg.MachineLabel)
		fmt.Println()

		for _, p := range cfg.Providers {
			var pp parsers.Parser
			switch p {
			case "claude_code":
				pp = claude_code.NewParser(filepath.Join(home, ".claude", "projects"))
			case "codex_cli":
				pp = codex_cli.NewParser(filepath.Join(home, ".codex", "sessions"))
			default:
				continue
			}
			aggs, _ := pp.Aggregate(tw)
			payload := map[string]any{
				"request_id":     uuid.New(),
				"agent_version":  Version,
				"parser_version": pp.ParserVersion(),
				"provider":       pp.Provider(),
				"events":         aggregatesToEvents(aggs),
			}
			b, _ := json.MarshalIndent(payload, "", "  ")
			fmt.Printf("--- %s ---\n%s\n", pp.Provider(), string(b))
		}
		return nil
	},
}

func init() {
	testCmd.Flags().BoolVar(&testShowTenant, "show-tenant", false, "Print full tenant slug")
	RegisterCommand(testCmd)
}
