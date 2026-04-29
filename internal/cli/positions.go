package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
)

func newPositionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "positions",
		Short: "Show open positions from DB",
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

			positions, err := repos.PositionRepository.GetOpen(ctx)
			if err != nil {
				return fmt.Errorf("query positions: %w", err)
			}

			if len(positions) == 0 {
				fmt.Println("No persisted positions found.")
				return nil
			}

			fmt.Println("Symbol\tSide\tEntry\tSize\tUnrealized PnL")
			fmt.Println("------\t----\t-----\t----\t-------------")
			for _, pos := range positions {
				fmt.Printf("%s\t%s\t%.4f\t%.4f\t%.2f\n",
					pos.Symbol, pos.Side, pos.EntryPrice, pos.Size, pos.UnrealizedPnL)
			}

			return nil
		},
	}
}
