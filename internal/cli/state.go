package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
)

func newStateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "state",
		Short: "Show current bot state",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			state := app.NewState(cfg.App.Mode)
			b, err := json.MarshalIndent(state, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal state: %w", err)
			}
			fmt.Println(string(b))
			return nil
		},
	}
}
