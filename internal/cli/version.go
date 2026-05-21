package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print binary version + commit",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("unitsense-agent %s (%s)\n", Version, Commit)
	},
}

func init() { RegisterCommand(versionCmd) }
