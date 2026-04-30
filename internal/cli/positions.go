package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
)

func newPositionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "positions",
		Short: "Show positions (open + today's closed) from DB",
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

			openPositions, err := repos.PositionRepository.GetOpen(ctx)
			if err != nil {
				return fmt.Errorf("query open positions: %w", err)
			}

			todayStart := time.Now().UTC().Truncate(24 * time.Hour)
			closedPositions, _ := repos.PositionRepository.GetClosed(ctx, todayStart)

			if len(openPositions) == 0 && len(closedPositions) == 0 {
				fmt.Println("No positions found.")
				return nil
			}

			if len(openPositions) > 0 {
				fmt.Println("--- Open Positions ---")
				fmt.Printf("%-12s %-8s %-12s %-10s %-12s %-12s\n", "Symbol", "Side", "Entry", "Size", "SL", "TP")
				fmt.Println("--------------------------------------------------")
				for _, pos := range openPositions {
					tpStr := "-"
					if pos.TakeProfit > 0 {
						tpStr = fmt.Sprintf("%.4f", pos.TakeProfit)
					}
					fmt.Printf("%-12s %-8s %-12.4f %-10.4f %-12.4f %-12s uPnL=$%.2f\n",
						pos.Symbol, pos.Side, pos.EntryPrice, pos.Size, pos.StopLoss, tpStr, pos.UnrealizedPnL)
				}
				fmt.Println()
			}

			if len(closedPositions) > 0 {
				fmt.Println("--- Today's Closed Positions ---")
				fmt.Printf("%-12s %-8s %-12s %-12s %-10s\n", "Symbol", "Side", "Entry", "Realized PnL", "Closed At")
				fmt.Println("--------------------------------------------------")
				for _, pos := range closedPositions {
					closedStr := "-"
					if pos.ClosedAt != nil {
						closedStr = pos.ClosedAt.Format("15:04 UTC")
					}
					fmt.Printf("%-12s %-8s %-12.4f $%-11.2f %-10s\n",
						pos.Symbol, pos.Side, pos.EntryPrice, pos.RealizedPnL, closedStr)
				}
			}

			return nil
		},
	}
}
