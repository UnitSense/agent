package cli

import (
	"fmt"
	"path/filepath"

	"github.com/UnitSense/agent/internal/config"
	"github.com/UnitSense/agent/internal/state"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print current config (redacted) + last run summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.DefaultPath()
		if err != nil {
			return err
		}
		cfg, err := config.Load(path)
		if err != nil {
			fmt.Printf("config: NOT CONFIGURED (%v)\n", err)
			return nil
		}
		tokenPrefix := cfg.DeviceToken
		if len(tokenPrefix) > 12 {
			tokenPrefix = tokenPrefix[:12]
		}
		fmt.Printf("server:        %s\n", cfg.ServerURL)
		fmt.Printf("tenant:        %s\n", cfg.Tenant)
		fmt.Printf("email:         %s\n", cfg.Email)
		fmt.Printf("machine_id:    %s\n", cfg.MachineID)
		fmt.Printf("machine_label: %s\n", cfg.MachineLabel)
		fmt.Printf("device_token:  %s***\n", tokenPrefix)
		fmt.Printf("providers:     %v\n", cfg.Providers)
		fmt.Printf("data_tier:     %s\n", cfg.DataTier)

		st, _ := state.Load(filepath.Join(filepath.Dir(path), "state.json"))
		fmt.Println()
		fmt.Println("Last run:")
		if st.LastRunAt == "" {
			fmt.Println("  (never)")
		} else {
			fmt.Printf("  at:         %s\n", st.LastRunAt)
			fmt.Printf("  status:     %s\n", st.LastRunStatus)
			fmt.Printf("  duration:   %dms\n", st.LastRunDurationMS)
			fmt.Printf("  sent:       %d events\n", st.LastRunEventsSent)
			fmt.Printf("  failures:   %d consecutive\n", st.ConsecutiveFailures)
			if st.LastError != "" {
				fmt.Printf("  last_error: %s\n", st.LastError)
			}
		}
		return nil
	},
}

func init() { RegisterCommand(statusCmd) }
