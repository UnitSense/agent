package cli

import (
	"fmt"

	"github.com/UnitSense/agent/internal/client"
	"github.com/UnitSense/agent/internal/config"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "POST /api/v1/agent/validate and print server response",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.DefaultPath()
		if err != nil {
			return err
		}
		cfg, err := config.Load(path)
		if err != nil {
			return err
		}
		cl := client.New(cfg.ServerURL, cfg.DeviceToken)
		v, err := cl.Validate()
		if err != nil {
			return err
		}
		fmt.Printf("valid:           %v\n", v.Valid)
		fmt.Printf("token_type:      %s\n", v.TokenType)
		fmt.Printf("tenant_slug:     %s\n", v.TenantSlug)
		fmt.Printf("developer_email: %s\n", v.DeveloperEmail)
		fmt.Printf("machine_label:   %s\n", v.MachineLabel)
		return nil
	},
}

func init() { RegisterCommand(validateCmd) }
