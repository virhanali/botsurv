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

func newReportCmd() *cobra.Command {
	var daysAgo int
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Show trading report from DB",
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

			return printDailyReport(ctx, repos, daysAgo)
		},
	}
	cmd.Flags().IntVarP(&daysAgo, "days", "d", 0, "Days ago (0 = today, 1 = yesterday, etc.)")
	return cmd
}

func printDailyReport(ctx context.Context, repos *db.Repositories, daysAgo int) error {
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -daysAgo)
	dayEnd := dayStart.Add(24 * time.Hour)

	fmt.Println("=== BotSurv Trading Report ===")
	fmt.Printf("Period: %s UTC\n", dayStart.Format("2006-01-02"))
	if daysAgo > 0 {
		fmt.Printf("(%d day(s) ago)\n", daysAgo)
	}
	fmt.Println()

	// Account snapshot closest to (but not after) period end
	snapshot, err := repos.AccountSnapshotRepository.GetLatestBefore(ctx, dayEnd)
	if err != nil || snapshot == nil {
		// Fallback to latest if no period-specific snapshot exists
		snapshot, err = repos.AccountSnapshotRepository.GetLatest(ctx)
		if err != nil || snapshot == nil {
			fmt.Println("No account snapshots found in DB.")
			return nil
		}
	}

	// Closed positions for the period
	closedPositions, err := repos.PositionRepository.GetClosed(ctx, dayStart)
	if err != nil {
		return fmt.Errorf("query closed positions: %w", err)
	}

	// Open positions
	openPositions, _ := repos.PositionRepository.GetOpen(ctx)

	// All orders for the period (for SL/TP detection)
	orders, err := repos.OrderRepository.GetAll(ctx, dayStart)
	if err != nil {
		return fmt.Errorf("query orders: %w", err)
	}

	// Build order lookup map
	orderMap := make(map[int64]*domain.Order)
	for i := range orders {
		orderMap[orders[i].ID] = &orders[i]
	}

	// Analyze closed positions
	var totalTrades, wins, losses, slCount, tpCount, manualClose int
	var totalRealizedPnL float64

	sourceBreakdown := make(map[string]int)
	for _, pos := range closedPositions {
		if pos.ClosedAt == nil || pos.ClosedAt.Before(dayStart) || !pos.ClosedAt.Before(dayEnd) {
			continue
		}
		totalTrades++

		if pos.RealizedPnL > 0 {
			wins++
		} else {
			losses++
		}
		totalRealizedPnL += pos.RealizedPnL

		// Detect close reason
		if pos.SLOrderID != nil {
			slOrder, ok := orderMap[*pos.SLOrderID]
			if ok && slOrder.Status == domain.OrderStatusFilled {
				slCount++
			}
		}
		if pos.TPOrderID != nil {
			tpOrder, ok := orderMap[*pos.TPOrderID]
			if ok && tpOrder.Status == domain.OrderStatusFilled {
				tpCount++
			}
		}
		if (pos.SLOrderID == nil || !isOrderFilled(orderMap, pos.SLOrderID)) &&
			(pos.TPOrderID == nil || !isOrderFilled(orderMap, pos.TPOrderID)) {
			manualClose++
		}

		// Source breakdown
		sourceBreakdown[pos.Source]++
	}

	fmt.Println("--- Account Summary ---")
	fmt.Printf("Balance:           $%.2f\n", snapshot.Balance)
	fmt.Printf("Available:         $%.2f\n", snapshot.AvailableBalance)
	fmt.Printf("Equity:            $%.2f\n", snapshot.Equity)
	fmt.Printf("Used Margin:       $%.2f\n", snapshot.UsedMargin)
	fmt.Println()

	if totalTrades == 0 {
		fmt.Println("--- No Trades Today ---")
		fmt.Println("No positions were closed during this period.")
	} else {
		fmt.Println("--- Trade Performance (Today's Closed) ---")
		fmt.Printf("Total Closed:      %d\n", totalTrades)
		fmt.Printf("Wins:              %d\n", wins)
		fmt.Printf("Losses:            %d\n", losses)
		winRate := float64(wins) / float64(totalTrades) * 100
		fmt.Printf("Win Rate:          %.1f%%\n", winRate)
		fmt.Printf("Today Realized PnL:$%.2f\n", totalRealizedPnL)
		fmt.Println()
		fmt.Println("--- Account Snapshot (period end) ---")
		fmt.Printf("Balance:           $%.2f\n", snapshot.Balance)
		fmt.Printf("Equity:            $%.2f\n", snapshot.Equity)
		fmt.Printf("Used Margin:       $%.2f\n", snapshot.UsedMargin)
		fmt.Printf("Cumul Realized PnL:$%.2f\n", snapshot.RealizedPnL)
		fmt.Printf("Unrealized PnL:    $%.2f\n", snapshot.UnrealizedPnL)
		fmt.Printf("Net PnL:           $%.2f  (realized + unrealized)\n", snapshot.RealizedPnL+snapshot.UnrealizedPnL)
		fmt.Printf("Total Fees:        $%.2f\n", snapshot.TotalFees)
		fmt.Printf("Total Slippage:    $%.2f\n", snapshot.TotalSlippage)
		fmt.Println()

		fmt.Println("--- Close Breakdown ---")
		fmt.Printf("SL Triggers:       %d\n", slCount)
		fmt.Printf("TP Triggers:       %d\n", tpCount)
		fmt.Printf("Manual/Other:      %d\n", manualClose)
		fmt.Println()

		if len(sourceBreakdown) > 0 {
			fmt.Println("--- Source Breakdown ---")
			for src, count := range sourceBreakdown {
				fmt.Printf("  %s: %d\n", src, count)
			}
			fmt.Println()
		}

		fmt.Println("--- Closed Trades ---")
		for _, pos := range closedPositions {
			if pos.ClosedAt == nil || pos.ClosedAt.Before(dayStart) || !pos.ClosedAt.Before(dayEnd) {
				continue
			}
			closeReason := "manual"
			if pos.SLOrderID != nil && isOrderFilled(orderMap, pos.SLOrderID) {
				closeReason = "SL"
			} else if pos.TPOrderID != nil && isOrderFilled(orderMap, pos.TPOrderID) {
				closeReason = "TP"
			}
			fmt.Printf("  %s %s entry=%.4f → %s PnL=$%.2f [%s]\n",
				pos.Symbol, pos.Side, pos.EntryPrice, pos.ClosedAt.Format("15:04 UTC"), pos.RealizedPnL, closeReason)
		}
	}

	fmt.Println()
	fmt.Println("--- Open Positions ---")
	fmt.Printf("Count:             %d\n", len(openPositions))
	for _, pos := range openPositions {
		fmt.Printf("  %s %s entry=%.4f size=%.4f uPnL=$%.2f SL=%.4f",
			pos.Symbol, pos.Side, pos.EntryPrice, pos.Size, pos.UnrealizedPnL, pos.StopLoss)
		if pos.TakeProfit > 0 {
			fmt.Printf(" TP=%.4f", pos.TakeProfit)
		}
		fmt.Println()
	}
	if len(openPositions) == 0 {
		fmt.Println("  (none)")
	}

	fmt.Printf("\nReported at: %s UTC\n", time.Now().UTC().Format("2006-01-02 15:04:05"))
	return nil
}

func isOrderFilled(orderMap map[int64]*domain.Order, orderID *int64) bool {
	if orderID == nil {
		return false
	}
	o, ok := orderMap[*orderID]
	return ok && o.Status == domain.OrderStatusFilled
}
