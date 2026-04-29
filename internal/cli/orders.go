package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
)

func newOrdersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "orders",
		Short: "Show open orders from DB",
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

			orders, err := repos.OrderRepository.GetOpen(ctx)
			if err != nil {
				return fmt.Errorf("query orders: %w", err)
			}

			if len(orders) == 0 {
				fmt.Println("No persisted orders found.")
				return nil
			}

			fmt.Println("Symbol\tType\tSide\tQty\tPrice\tStop\tStatus")
			fmt.Println("------\t----\t----\t---\t-----\t----\t------")
			for _, o := range orders {
				priceStr := "-"
				if o.Price != nil {
					priceStr = fmt.Sprintf("%.4f", *o.Price)
				}
				stopStr := "-"
				if o.StopPrice != nil {
					stopStr = fmt.Sprintf("%.4f", *o.StopPrice)
				}
				fmt.Printf("%s\t%s\t%s\t%.4f\t%s\t%s\t%s\n",
					o.Symbol, o.OrderType, o.Side, o.Qty, priceStr, stopStr, o.Status)
			}

			return nil
		},
	}
}
