package cli

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/UnitSense/agent/internal/client"
	"github.com/UnitSense/agent/internal/config"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure the agent (interactive)",
	RunE:  runSetup,
}

var (
	setupServerURL      string
	setupTenant         string
	setupEmail          string
	setupTokenIn        bool
	setupEnableGitHints bool
)

func init() {
	setupCmd.Flags().StringVar(&setupServerURL, "server", "https://app.unitsense.ai", "UnitSense server URL")
	setupCmd.Flags().StringVar(&setupTenant, "tenant", "", "Tenant slug (will prompt if empty)")
	setupCmd.Flags().StringVar(&setupEmail, "email", "", "Developer email (will prompt if empty)")
	setupCmd.Flags().BoolVar(&setupTokenIn, "token-stdin", false, "Read registration token from stdin (no echo)")
	setupCmd.Flags().BoolVar(&setupEnableGitHints, "enable-git-hints", false, "Opt in to local git hints (branch, commit SHAs, hashed remote URL) in session payloads")
	RegisterCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	reader := bufio.NewReader(os.Stdin)

	tenant := setupTenant
	if tenant == "" {
		fmt.Print("Tenant slug: ")
		s, _ := reader.ReadString('\n')
		tenant = strings.TrimSpace(s)
	}
	if tenant == "" {
		return errors.New("tenant slug required")
	}

	email := setupEmail
	if email == "" {
		fmt.Print("Developer email: ")
		s, _ := reader.ReadString('\n')
		email = strings.TrimSpace(s)
	}
	if email == "" {
		return errors.New("email required")
	}

	regToken := os.Getenv("UNITSENSE_TOKEN")
	if regToken == "" && setupTokenIn {
		s, _ := reader.ReadString('\n')
		regToken = strings.TrimSpace(s)
	}
	if regToken == "" {
		fmt.Print("Registration token (input hidden): ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return err
		}
		fmt.Println()
		regToken = strings.TrimSpace(string(b))
	}
	if !strings.HasPrefix(regToken, "ust_reg_") {
		return errors.New("registration token must start with ust_reg_")
	}

	u, err := url.Parse(setupServerURL)
	if err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || strings.HasPrefix(u.Hostname(), "127.")))) {
		return fmt.Errorf("server URL must be HTTPS (or http localhost): %s", setupServerURL)
	}

	machineID := uuid.New()
	hostname, _ := os.Hostname()

	cl := client.New(setupServerURL, regToken)
	v, err := cl.Validate()
	if err != nil {
		return fmt.Errorf("token validate failed: %w", err)
	}
	if v.TokenType != "registration" {
		return fmt.Errorf("expected registration token, got %s", v.TokenType)
	}
	if v.TenantSlug != tenant {
		return fmt.Errorf("token belongs to tenant %q, not %q", v.TenantSlug, tenant)
	}

	regRes, err := cl.Register(client.RegisterRequest{
		DeveloperEmail: email,
		MachineID:      machineID,
		MachineLabel:   hostname,
		AgentVersion:   Version,
	})
	if err != nil {
		return fmt.Errorf("register failed: %w", err)
	}

	cfg := &config.Config{
		ServerURL:      setupServerURL,
		DeviceToken:    regRes.DeviceToken,
		Tenant:         regRes.TenantSlug,
		Email:          email,
		MachineID:      machineID,
		MachineLabel:   hostname,
		Providers:      []string{"claude_code", "codex_cli"},
		DataTier:       "metrics",
		JitterSeconds:  30,
		EnableGitHints: setupEnableGitHints,
	}
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	fmt.Printf("Setup complete. Device registered as %s (developer_id=%s).\n", hostname, regRes.DeveloperID)
	fmt.Printf("Config saved to %s\n", path)
	if setupEnableGitHints {
		fmt.Println("Git hints enabled: branch name, commit SHAs, and hashed remote URL will be included in session payloads.")
	}
	fmt.Println("\nNext: schedule periodic sync — `unitsense-agent install --schedule=10m`")
	return nil
}
