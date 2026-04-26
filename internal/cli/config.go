package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
)

func newConfigValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "config validate",
		Short: "Validate the configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				fmt.Printf("Config validation FAILED: %v\n", err)
				return err
			}
			fmt.Printf("Config validation PASSED: %s\n", configPath)
			fmt.Printf("  Mode: %s\n", cfg.App.Mode)
			fmt.Printf("  Database: %s\n", cfg.Database.Driver)
			fmt.Printf("  Broker: %s\n", cfg.Broker.Provider)
			fmt.Printf("  LLM Enabled: %v\n", cfg.LLM.Enabled)
			fmt.Printf("  LLM Model: %s\n", cfg.LLM.Model)
			fmt.Printf("  Max Open Positions: %d\n", cfg.PortfolioRisk.MaxOpenPositions)
			fmt.Printf("  Max Daily Loss: %.2f%%\n", cfg.PortfolioRisk.MaxDailyLossPct)
			return nil
		},
	}
}
