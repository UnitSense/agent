package cli

import (
	"os"
	"time"

	"github.com/UnitSense/agent/internal/schedule"
	"github.com/spf13/cobra"
)

var installSchedule string

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Register the agent with the OS scheduler",
	RunE: func(cmd *cobra.Command, args []string) error {
		bin, err := os.Executable()
		if err != nil {
			return err
		}
		interval, err := time.ParseDuration(installSchedule)
		if err != nil {
			return err
		}
		return schedule.Install(bin, interval)
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the scheduler entry (keeps config)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return schedule.Uninstall()
	},
}

func init() {
	installCmd.Flags().StringVar(&installSchedule, "schedule", "10m", "Run interval (e.g. 10m, 1h)")
	RegisterCommand(installCmd)
	RegisterCommand(uninstallCmd)
}
