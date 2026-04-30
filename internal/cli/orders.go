package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
)

func newOrdersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "orders",
		Short: "Show recent orders from DB",
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

			// Show all orders from the last 48h
			since := time.Now().UTC().Add(-48 * time.Hour)
			orders, err := repos.OrderRepository.GetAll(ctx, since)
			if err != nil {
				return fmt.Errorf("query orders: %w", err)
			}

			if len(orders) == 0 {
				fmt.Println("No orders found in the last 48 hours.")
				return nil
			}

			openOrders, _ := repos.OrderRepository.GetOpen(ctx)

			if len(openOrders) > 0 {
				fmt.Println("--- Open Orders ---")
				fmt.Printf("%-12s %-18s %-8s %-10s %-8s %-12s %-14s\n", "Symbol", "Type", "Side", "Qty", "Price", "Stop", "Status")
				fmt.Println("--------------------------------------------------")
				for _, o := range openOrders {
					printOrder(o)
				}
				fmt.Println()
			}

			if len(orders) > 0 {
				fmt.Println("--- All Recent Orders ---")
				fmt.Printf("%-12s %-18s %-8s %-10s %-8s %-12s %-14s\n", "Symbol", "Type", "Side", "Qty", "Price", "Stop", "Status")
				fmt.Println("--------------------------------------------------")
				for _, o := range orders {
					printOrder(o)
				}
			}

			return nil
		},
	}
}

func printOrder(o domain.Order) {
	priceStr := "-"
	if o.Price != nil {
		priceStr = fmt.Sprintf("%.4f", *o.Price)
	}
	stopStr := "-"
	if o.StopPrice != nil {
		stopStr = fmt.Sprintf("%.4f", *o.StopPrice)
	}
	fmt.Printf("%-12s %-18s %-8s %-10.4f %-8s %-12s %-14s\n",
		o.Symbol, o.OrderType, o.Side, o.Qty, priceStr, stopStr, o.Status)
}
