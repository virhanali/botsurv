package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

func newSimulateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "paper-simulate",
		Short: "Simulate a paper trade lifecycle for verification (no market data needed)",
		Long: `Simulates a complete paper trade lifecycle:
1. Open market long with SL/TP
2. Trigger SL via price update
3. Show full traceability: order -> execution -> position -> SL/TP -> close -> PnL

Use --scenario to choose: sl, tp, sl-first-candle.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			scenario, _ := cmd.Flags().GetString("scenario")
			return runSimulate(scenario)
		},
	}
	cmd.Flags().String("scenario", "", "Scenario: sl, tp, or sl-first-candle")
	return cmd
}

func runSimulate(scenario string) error {
	cfg := app.PaperConfig{
		StartingBalanceUSD: 1000,
		FeeMakerBps:        2,
		FeeTakerBps:        5.5,
		SlippageBps:        5,
		DefaultLeverage:    5,
	}
	log := logger.New(os.Stderr, logger.LevelDebug)
	pb := broker.NewPaperBroker(cfg, log)

	// Simulate: update price then place market buy
	pb.UpdatePrice("BTCUSDT", 65000)

	entryReq := broker.OrderRequest{
		Symbol:     "BTCUSDT",
		Side:       domain.OrderSideBuy,
		OrderType:  domain.OrderTypeMarket,
		Qty:        0.01,
		StopLoss:   64000,
		TakeProfit: 67000,
	}

	order, err := pb.PlaceOrder(context.Background(), entryReq)
	if err != nil {
		return fmt.Errorf("place entry order: %w", err)
	}
	fmt.Printf("ENTRY order: %s status=%s\n", order.BrokerOrderID, order.Status)

	state, _ := pb.GetAccountState(context.Background())
	fmt.Printf("  Balance: $%.2f Available: $%.2f Margin: $%.2f Fees: $%.4f\n",
		state.Balance, state.AvailableBalance, state.UsedMargin, state.TotalFees)

	pos, ok := pb.GetPosition("BTCUSDT")
	if !ok {
		return fmt.Errorf("no position after entry")
	}
	fmt.Printf("  Position: %s %s entry=%.2f size=%.4f SL=%.2f TP=%.2f SLOrderID=%v TPOrderID=%v\n",
		pos.Symbol, pos.Side, pos.EntryPrice, pos.Size, pos.StopLoss, pos.TakeProfit,
		pos.SLOrderID != nil, pos.TPOrderID != nil)

	orders, _ := pb.GetOpenOrders(context.Background())
	fmt.Printf("  Open orders: %d (SL/TP)\n", len(orders))

	// Execute the scenario
	switch scenario {
	case "sl":
		fmt.Println("\n--- Triggering SL ---")
		pb.UpdatePrice("BTCUSDT", 63900)
		fmt.Println("  Price dropped to 63900 (SL at 64000)")
	case "tp":
		fmt.Println("\n--- Triggering TP ---")
		pb.UpdatePrice("BTCUSDT", 67100)
		fmt.Println("  Price rose to 67100 (TP at 67000)")
	case "sl-first-candle":
		fmt.Println("\n--- Same-candle SL/TP (conservative SL-first) ---")
		pb.ProcessCandle(domain.Candle{
			Symbol: "BTCUSDT",
			High:   67500,
			Low:    63500,
			Close:  65500,
		})
		fmt.Println("  Candle High=67500 Low=63500 Close=65500")
	default:
		fmt.Println("\n--- Run with --scenario=sl, --scenario=tp, or --scenario=sl-first-candle ---")
		return nil
	}

	// Check after scenario
	openPos, _ := pb.GetOpenPositions(context.Background())
	closedPos := pb.GetClosedPositions()
	state, _ = pb.GetAccountState(context.Background())

	if len(openPos) == 0 && len(closedPos) > 0 {
		cp := closedPos[len(closedPos)-1]
		fmt.Printf("\nTRACE: position closed\n")
		fmt.Printf("  Entry: $%.2f  PnL: $%.2f  Reason: %s\n",
			cp.EntryPrice, cp.RealizedPnL, closeReason(&cp, orders))
	}
	fmt.Printf("\nFinal state:\n")
	fmt.Printf("  Balance: $%.2f  Equity: $%.2f  Realized PnL: $%.2f\n",
		state.Balance, state.Equity, state.RealizedPnL)
	fmt.Printf("  Total Fees: $%.4f  Total Slippage: $%.4f\n",
		state.TotalFees, state.TotalSlippage)
	fmt.Printf("  Open positions: %d  Closed positions: %d\n",
		len(openPos), len(closedPos))

	return nil
}

func closeReason(pos *domain.Position, orders []domain.Order) string {
	if pos.SLOrderID != nil {
		for _, o := range orders {
			if o.ID == *pos.SLOrderID && o.Status == domain.OrderStatusFilled {
				return "SL"
			}
		}
	}
	if pos.TPOrderID != nil {
		for _, o := range orders {
			if o.ID == *pos.TPOrderID && o.Status == domain.OrderStatusFilled {
				return "TP"
			}
		}
	}
	return "manual"
}
