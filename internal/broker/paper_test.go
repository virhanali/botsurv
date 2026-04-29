package broker

import (
	"context"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

// --- DB Repo Mocks for testing persistence ---

type mockPositionRepo struct {
	positions []domain.Position
}

func (m *mockPositionRepo) Insert(ctx context.Context, p domain.Position) (int64, error) {
	m.positions = append(m.positions, p)
	return int64(len(m.positions)), nil
}

func (m *mockPositionRepo) Update(ctx context.Context, p domain.Position) error {
	for i := range m.positions {
		if m.positions[i].ID == p.ID {
			m.positions[i] = p
			return nil
		}
	}
	return nil
}

func (m *mockPositionRepo) GetOpen(ctx context.Context) ([]domain.Position, error) {
	var open []domain.Position
	for _, p := range m.positions {
		if p.Status == domain.PositionStatusOpen {
			open = append(open, p)
		}
	}
	return open, nil
}

func (m *mockPositionRepo) GetClosed(ctx context.Context, since time.Time) ([]domain.Position, error) {
	return nil, nil
}

func (m *mockPositionRepo) GetBySymbol(ctx context.Context, symbol string) (*domain.Position, error) {
	return nil, nil
}

type mockOrderRepo struct {
	orders []domain.Order
}

func (m *mockOrderRepo) Insert(ctx context.Context, o domain.Order) (int64, error) {
	m.orders = append(m.orders, o)
	return int64(len(m.orders)), nil
}

func (m *mockOrderRepo) Update(ctx context.Context, o domain.Order) error {
	for i := range m.orders {
		if m.orders[i].ID == o.ID {
			m.orders[i] = o
			return nil
		}
	}
	return nil
}

func (m *mockOrderRepo) GetOpen(ctx context.Context) ([]domain.Order, error) {
	return nil, nil
}

func (m *mockOrderRepo) GetBySymbol(ctx context.Context, symbol string) ([]domain.Order, error) {
	return nil, nil
}

type mockExecRepo struct {
	executions []domain.Execution
}

func (m *mockExecRepo) Insert(ctx context.Context, e domain.Execution) (int64, error) {
	m.executions = append(m.executions, e)
	return int64(len(m.executions)), nil
}

type mockAcctSnapRepo struct {
	snapshots []domain.AccountState
}

func (m *mockAcctSnapRepo) Insert(ctx context.Context, a domain.AccountState) (int64, error) {
	m.snapshots = append(m.snapshots, a)
	return int64(len(m.snapshots)), nil
}

func newTestBroker() *PaperBroker {
	cfg := app.PaperConfig{
		StartingBalanceUSD: 1000,
		FeeMakerBps:        2,
		FeeTakerBps:        5.5,
		SlippageBps:        5,
		DefaultLeverage:    5,
	}
	log := logger.New(nil, logger.LevelDebug)
	return NewPaperBroker(cfg, log)
}

func TestGetAccountState_InitialBalance(t *testing.T) {
	pb := newTestBroker()
	state, err := pb.GetAccountState(context.Background())
	if err != nil {
		t.Fatalf("GetAccountState: %v", err)
	}
	if state.Balance != 1000 {
		t.Errorf("expected balance 1000, got %.2f", state.Balance)
	}
	if state.AvailableBalance != 1000 {
		t.Errorf("expected available 1000, got %.2f", state.AvailableBalance)
	}
	if state.UsedMargin != 0 {
		t.Errorf("expected margin 0, got %.2f", state.UsedMargin)
	}
}

func TestPlaceOrder_MarketBuyLong(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	order, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("expected filled, got %s", order.Status)
	}

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	pos := positions[0]
	if pos.Symbol != "BTCUSDT" {
		t.Errorf("expected BTCUSDT, got %s", pos.Symbol)
	}
	if pos.Side != domain.SideLong {
		t.Errorf("expected LONG, got %s", pos.Side)
	}
	if pos.Size != 0.01 {
		t.Errorf("expected size 0.01, got %.4f", pos.Size)
	}
}

func TestPlaceOrder_MarketSellShort(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("ETHUSDT", 3000)

	order, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "ETHUSDT",
		Side:      domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  3100,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("expected filled, got %s", order.Status)
	}

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if positions[0].Side != domain.SideShort {
		t.Errorf("expected SHORT, got %s", positions[0].Side)
	}
}

func TestPlaceOrder_InsufficientMargin(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	// Try to buy 1 BTC at 65000 with 5x leverage = $13000 margin needed
	// But we only have $1000
	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  64000,
	})
	if err == nil {
		t.Error("expected error for insufficient margin")
	}
}

func TestPlaceOrder_FeeCalculation(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	state, _ := pb.GetAccountState(context.Background())
	// notional = 65000 * 0.01 * (1 + 5bps slippage) ≈ 650.325
	// fee = notional * 5.5bps ≈ 0.3577
	if state.TotalFees <= 0 {
		t.Error("expected fees > 0")
	}
	if state.TotalFees > 1 {
		t.Errorf("fees seem too high: %.4f", state.TotalFees)
	}
}

func TestPlaceOrder_SlippageCalculation(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	state, _ := pb.GetAccountState(context.Background())
	if state.TotalSlippage <= 0 {
		t.Error("expected slippage > 0")
	}
}

func TestPlaceOrder_MarginTracking(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	state, _ := pb.GetAccountState(context.Background())
	if state.UsedMargin <= 0 {
		t.Error("expected used margin > 0")
	}
	// notional ≈ 650.325, margin = notional / 5 ≈ 130.065
	if state.UsedMargin < 100 || state.UsedMargin > 200 {
		t.Errorf("unexpected margin: %.2f", state.UsedMargin)
	}
	expectedAvailable := state.Balance - state.UsedMargin
	if state.AvailableBalance != expectedAvailable {
		t.Errorf("available %.2f != balance %.2f - margin %.2f", state.AvailableBalance, state.Balance, state.UsedMargin)
	}
}

func TestClosePosition_LongProfit(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Price goes up
	pb.UpdatePrice("BTCUSDT", 66000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	state, _ := pb.GetAccountState(context.Background())
	// Profit ≈ (66000-65000) * 0.01 - fees ≈ 10 - fees
	if state.RealizedPnL <= 0 {
		t.Errorf("expected positive PnL, got %.4f", state.RealizedPnL)
	}
	if len(pb.closedPositions) != 1 {
		t.Errorf("expected 1 closed position, got %d", len(pb.closedPositions))
	}
}

func TestClosePosition_LongLoss(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Price goes down
	pb.UpdatePrice("BTCUSDT", 64000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	state, _ := pb.GetAccountState(context.Background())
	if state.RealizedPnL >= 0 {
		t.Errorf("expected negative PnL, got %.4f", state.RealizedPnL)
	}
}

func TestClosePosition_ShortProfit(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("ETHUSDT", 3000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "ETHUSDT",
		Side:      domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  3100,
	})

	// Price goes down (profit for short)
	pb.UpdatePrice("ETHUSDT", 2900)
	pb.ClosePosition(context.Background(), "ETHUSDT")

	state, _ := pb.GetAccountState(context.Background())
	if state.RealizedPnL <= 0 {
		t.Errorf("expected positive PnL for short, got %.4f", state.RealizedPnL)
	}
}

func TestStopLoss_Triggers(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Set SL at 64000
	slPrice := 64000.0
	pb.SetProtectiveOrders(context.Background(), "BTCUSDT", slPrice, 0)

	// Price drops to SL
	pb.UpdatePrice("BTCUSDT", 63900)

	// Position should be closed
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 open positions after SL, got %d", len(positions))
	}
	if len(pb.closedPositions) != 1 {
		t.Errorf("expected 1 closed position, got %d", len(pb.closedPositions))
	}
}

func TestTakeProfit_Triggers(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Set TP at 67000
	tpPrice := 67000.0
	slPrice := 64000.0
	pb.SetProtectiveOrders(context.Background(), "BTCUSDT", slPrice, tpPrice)

	// Price reaches TP
	pb.UpdatePrice("BTCUSDT", 67100)

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions after TP, got %d", len(positions))
	}
}

func TestSameCandle_SLFirst(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	slPrice := 64000.0
	tpPrice := 67000.0
	pb.SetProtectiveOrders(context.Background(), "BTCUSDT", slPrice, tpPrice)

	// Candle touches both SL and TP
	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   67500,
		Low:    63500,
		Close:  65500,
	})

	// Conservative assumption: SL triggers first
	if len(pb.closedPositions) != 1 {
		t.Fatalf("expected 1 closed position, got %d", len(pb.closedPositions))
	}
	// PnL should reflect SL exit, not TP
	if pb.closedPositions[0].RealizedPnL > 0 {
		t.Error("conservative SL-first: PnL should be negative")
	}
}

func TestEmergencyCloseAll(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)
	pb.UpdatePrice("ETHUSDT", 3000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  3100,
	})
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "ETHUSDT",
		Side:      domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  3100,
	})

	pb.EmergencyCloseAll(context.Background())

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions after emergency close, got %d", len(positions))
	}
	if len(pb.closedPositions) != 2 {
		t.Errorf("expected 2 closed positions, got %d", len(pb.closedPositions))
	}
}

func TestSetProtectiveOrders_ValidateSL(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// SL above entry for LONG is invalid
	err := pb.SetProtectiveOrders(context.Background(), "BTCUSDT", 66000, 0)
	if err == nil {
		t.Error("expected error for SL above entry on LONG")
	}

	// SL at 0 is invalid
	err = pb.SetProtectiveOrders(context.Background(), "BTCUSDT", 0, 0)
	if err == nil {
		t.Error("expected error for SL at 0")
	}
}

func TestSetProtectiveOrders_ValidateTP(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// TP below entry for LONG is invalid
	err := pb.SetProtectiveOrders(context.Background(), "BTCUSDT", 64000, 64000)
	if err == nil {
		t.Error("expected error for TP below entry on LONG")
	}
}

func TestLimitOrder_Fill(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  63000,
		Price:     &price,
	})

	orders, _ := pb.GetOpenOrders(context.Background())
	if len(orders) != 1 {
		t.Fatalf("expected 1 pending order, got %d", len(orders))
	}

	// Candle touches limit price
	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   65000,
		Low:    63500,
		Close:  64500,
	})

	// Should be filled with protective orders created
	orders, _ = pb.GetOpenOrders(context.Background())
	if len(orders) < 1 {
		t.Errorf("expected at least 1 protective order after limit fill, got %d", len(orders))
	}
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 1 {
		t.Errorf("expected 1 position after limit fill, got %d", len(positions))
	}
}

func TestLimitOrder_NoFill(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  63000,
		Price:     &price,
	})

	// Candle doesn't touch limit price
	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   65500,
		Low:    64200,
		Close:  65000,
	})

	orders, _ := pb.GetOpenOrders(context.Background())
	if len(orders) != 1 {
		t.Errorf("expected 1 pending order (not filled), got %d", len(orders))
	}
}

func TestCancelOrder(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	order, _ := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  63000,
		Price:     &price,
	})

	err := pb.CancelOrder(context.Background(), order.BrokerOrderID)
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}

	orders, _ := pb.GetOpenOrders(context.Background())
	if len(orders) != 0 {
		t.Errorf("expected 0 orders after cancel, got %d", len(orders))
	}
}

func TestUnrealizedPnL(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Price goes up
	pb.UpdatePrice("BTCUSDT", 66000)

	state, _ := pb.GetAccountState(context.Background())
	if state.UnrealizedPnL <= 0 {
		t.Errorf("expected positive unrealized PnL, got %.4f", state.UnrealizedPnL)
	}
	// (66000-65000) * 0.01 = 10
	if state.UnrealizedPnL < 9 || state.UnrealizedPnL > 11 {
		t.Errorf("unexpected unrealized PnL: %.4f", state.UnrealizedPnL)
	}
}

func TestResetDailyLoss(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Close at loss
	pb.UpdatePrice("BTCUSDT", 64000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	state, _ := pb.GetAccountState(context.Background())
	if state.DailyLoss <= 0 {
		t.Error("expected daily loss > 0")
	}

	pb.ResetDailyLoss()
	state, _ = pb.GetAccountState(context.Background())
	if state.DailyLoss != 0 {
		t.Errorf("expected daily loss 0 after reset, got %.4f", state.DailyLoss)
	}
}

func TestPlaceOrder_RejectInvalidSL_LongSLAboveEntry(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  66000, // invalid: LONG SL above entry
	})
	if err == nil {
		t.Fatal("expected error for LONG SL above entry")
	}
	// Position was never opened (rejected at broker layer)
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

func TestPlaceOrder_RejectInvalidSL_ShortSLBelowEntry(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("ETHUSDT", 3000)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "ETHUSDT",
		Side:      domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  2900, // invalid: SHORT SL below entry
	})
	if err == nil {
		t.Fatal("expected error for SHORT SL below entry")
	}
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

func TestPlaceOrder_InvalidSL_NoDirtyState(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	stateBefore, _ := pb.GetAccountState(context.Background())

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  66000, // invalid LONG SL
	})
	if err == nil {
		t.Fatal("expected error")
	}

	// No position, no closed position, no fee, no margin change
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 open positions, got %d", len(positions))
	}
	if len(pb.closedPositions) != 0 {
		t.Errorf("expected 0 closed positions, got %d", len(pb.closedPositions))
	}

	stateAfter, _ := pb.GetAccountState(context.Background())
	if stateAfter.Balance != stateBefore.Balance {
		t.Errorf("balance changed: %.4f -> %.4f", stateBefore.Balance, stateAfter.Balance)
	}
	if stateAfter.UsedMargin != stateBefore.UsedMargin {
		t.Errorf("margin changed: %.4f -> %.4f", stateBefore.UsedMargin, stateAfter.UsedMargin)
	}
	if stateAfter.TotalFees != stateBefore.TotalFees {
		t.Errorf("fees changed: %.4f -> %.4f", stateBefore.TotalFees, stateAfter.TotalFees)
	}
}

func TestLimitOrder_Fill_InvalidSL_NoDirtyState(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	stateBefore, _ := pb.GetAccountState(context.Background())

	price := 64000.0
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  66000, // invalid LONG SL
		Price:     &price,
	})

	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   65000,
		Low:    63500,
		Close:  64500,
	})

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 open positions, got %d", len(positions))
	}
	if len(pb.closedPositions) != 0 {
		t.Errorf("expected 0 closed positions, got %d", len(pb.closedPositions))
	}

	stateAfter, _ := pb.GetAccountState(context.Background())
	if stateAfter.Balance != stateBefore.Balance {
		t.Errorf("balance changed: %.4f -> %.4f", stateBefore.Balance, stateAfter.Balance)
	}
	if stateAfter.UsedMargin != stateBefore.UsedMargin {
		t.Errorf("margin changed: %.4f -> %.4f", stateBefore.UsedMargin, stateAfter.UsedMargin)
	}
	if stateAfter.TotalFees != stateBefore.TotalFees {
		t.Errorf("fees changed: %.4f -> %.4f", stateBefore.TotalFees, stateAfter.TotalFees)
	}
}

func TestLimitOrder_Fill_InvalidIntendedSL_Rejected(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	order, _ := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  66000, // invalid intended SL for LONG (above entry)
		Price:     &price,
	})

	// Candle touches limit price
	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   65000,
		Low:    63500,
		Close:  64500,
	})

	// Order should be rejected, position never opened.
	// Look up the updated order from the broker (PlaceOrder returns a copy).
	orders, _ := pb.GetOpenOrders(context.Background())
	var found *domain.Order
	for i := range orders {
		if orders[i].BrokerOrderID == order.BrokerOrderID {
			found = &orders[i]
			break
		}
	}
	if found == nil {
		// Order may have been removed from openOrders when rejected — that's also acceptable.
	} else if found.Status != domain.OrderStatusRejected {
		t.Errorf("expected rejected, got %s", found.Status)
	}
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

// --- DB Persistence Tests ---

func TestPlaceOrder_MarketBuy_PersistsFilledOrderAndPositionWithSL(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	posRepo := &mockPositionRepo{}
	ordRepo := &mockOrderRepo{}
	execRepo := &mockExecRepo{}
	snapRepo := &mockAcctSnapRepo{}
	pb.SetPositionRepo(posRepo)
	pb.SetOrderRepo(ordRepo)
	pb.SetExecutionRepo(execRepo)
	pb.SetAccountSnapshotRepo(snapRepo)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	foundMain := false
	for _, o := range ordRepo.orders {
		if o.OrderType == domain.OrderTypeMarket && o.Status == domain.OrderStatusFilled {
			foundMain = true
		}
	}
	if !foundMain {
		t.Error("expected filled market order persisted")
	}

	if len(posRepo.positions) == 0 {
		t.Fatal("expected at least 1 position persisted")
	}
	pos := posRepo.positions[0]
	if pos.StopLoss != 64000 {
		t.Errorf("expected StopLoss=64000, got %f", pos.StopLoss)
	}

	foundSL := false
	for _, o := range ordRepo.orders {
		if o.OrderType == domain.OrderTypeStopMarket {
			foundSL = true
		}
	}
	if !foundSL {
		t.Error("expected protective STOP_MARKET order persisted")
	}
}

func TestPlaceOrder_InvalidSL_DoesNotLeaveOpenPositionInDB(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	posRepo := &mockPositionRepo{}
	ordRepo := &mockOrderRepo{}
	execRepo := &mockExecRepo{}
	snapRepo := &mockAcctSnapRepo{}
	pb.SetPositionRepo(posRepo)
	pb.SetOrderRepo(ordRepo)
	pb.SetExecutionRepo(execRepo)
	pb.SetAccountSnapshotRepo(snapRepo)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  66000, // invalid LONG SL above entry
	})
	if err == nil {
		t.Fatal("expected error for invalid SL")
	}

	for _, p := range posRepo.positions {
		if p.Status == domain.PositionStatusOpen {
			t.Errorf("unexpected open position in DB after invalid SL: %+v", p)
		}
	}
}
