package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
)

func newReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Show historical account report from DB",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			database, err := db.Open(cfg.Database.Driver, cfg.Database.DSN,
				cfg.Database.Pool.MaxOpenConns, cfg.Database.Pool.MaxIdleConns, cfg.Database.Pool.ConnMaxLifetime)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer database.Close()

			repos := db.NewPostgresRepositories(database)
			ctx := context.Background()

			fmt.Println("=== BotSurv Historical Report (from DB) ===")
			fmt.Println("NOTE: This shows persisted historical data, not the live running bot state.")

			// Query latest account snapshot
			snapshot, err := repos.AccountSnapshotRepository.GetLatest(ctx)
			if err != nil || snapshot == nil {
				fmt.Println("\nNo account snapshots found in DB.")
				return nil
			}

			fmt.Printf("\nBalance:        $%.2f\n", snapshot.Balance)
			fmt.Printf("Available:      $%.2f\n", snapshot.AvailableBalance)
			fmt.Printf("Equity:         $%.2f\n", snapshot.Equity)
			fmt.Printf("Used Margin:    $%.2f\n", snapshot.UsedMargin)
			fmt.Printf("Realized PnL:   $%.2f\n", snapshot.RealizedPnL)
			fmt.Printf("Unrealized PnL: $%.2f\n", snapshot.UnrealizedPnL)
			fmt.Printf("Total Fees:     $%.2f\n", snapshot.TotalFees)
			fmt.Printf("Total Slippage: $%.2f\n", snapshot.TotalSlippage)
			fmt.Printf("Daily Loss:     $%.2f\n", snapshot.DailyLoss)
			fmt.Printf("Recorded At:    %s\n", snapshot.RecordedAt.Format("2006-01-02 15:04:05 UTC"))

			// Query open positions
			openPositions, _ := repos.PositionRepository.GetOpen(ctx)
			fmt.Printf("\nOpen Positions: %d\n", len(openPositions))
			for _, pos := range openPositions {
				fmt.Printf("  %s %s entry=%.4f size=%.4f uPnL=%.2f\n",
					pos.Symbol, pos.Side, pos.EntryPrice, pos.Size, pos.UnrealizedPnL)
			}

			return nil
		},
	}
}
