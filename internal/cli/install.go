package cli

import (
	"os"
	"path/filepath"
	"time"

	"github.com/UnitSense/agent/internal/config"
	"github.com/UnitSense/agent/internal/schedule"
	"github.com/UnitSense/agent/internal/state"
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
		if err := schedule.Install(bin, interval); err != nil {
			return err
		}

		cfgPath, err := config.DefaultPath()
		if err != nil {
			return err
		}
		statePath := filepath.Join(filepath.Dir(cfgPath), "state.json")
		st, _ := state.Load(statePath)
		st.ScheduledIntervalMinutes = int(interval.Minutes())
		return state.Save(statePath, st)
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
