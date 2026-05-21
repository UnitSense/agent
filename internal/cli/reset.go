package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UnitSense/agent/internal/config"
	"github.com/spf13/cobra"
)

var resetForce bool

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Delete agent config + state (requires confirm)",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.DefaultPath()
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		if !resetForce {
			fmt.Printf("This will remove %s. Continue? [y/N] ", dir)
			r := bufio.NewReader(os.Stdin)
			ans, _ := r.ReadString('\n')
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ans)), "y") {
				return fmt.Errorf("cancelled")
			}
		}
		return os.RemoveAll(dir)
	},
}

func init() {
	resetCmd.Flags().BoolVar(&resetForce, "force", false, "Skip confirmation")
	RegisterCommand(resetCmd)
}
