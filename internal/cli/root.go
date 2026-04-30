package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
)

var configPath string

// NewRootCommand creates the root cobra command.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "bot",
		Short: "BotSurv - Agentic AI Crypto Futures Trading Bot",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := app.LoadEnv(); err != nil {
				return fmt.Errorf("load .env: %w", err)
			}
			return nil
		},
	}
	root.PersistentFlags().StringVar(&configPath, "config", "configs/paper.yaml", "path to config file")
	root.AddCommand(newInitCommand())
	root.AddCommand(newConfigValidateCommand())
	root.AddCommand(newStateCommand())
	root.AddCommand(newUniverseCmd())
	root.AddCommand(newRunOnceCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newReportCmd())
	root.AddCommand(newPositionsCmd())
	root.AddCommand(newOrdersCmd())
	root.AddCommand(newSimulateCmd())
	return root
}
